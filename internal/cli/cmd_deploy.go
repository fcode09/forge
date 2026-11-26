package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/fcode09/forge/internal/artifact"
	"github.com/fcode09/forge/internal/config"
	"github.com/fcode09/forge/internal/deploy"
	"github.com/fcode09/forge/internal/logging"
	"github.com/fcode09/forge/internal/release"
	"github.com/fcode09/forge/internal/remote"
	"github.com/fcode09/forge/internal/transport"
)

// Exit codes specific to deploy, so automation can tell the outcomes apart.
const (
	// ExitRolledBack: the deploy failed and the previous version is running again.
	ExitRolledBack = 3
	// ExitRollbackFailed: the deploy failed and the previous state could not be confirmed; a person must look.
	ExitRollbackFailed = 4
)

func (a *App) cmdDeploy(ctx context.Context, args []string) int {
	fs := a.newFlags("deploy")
	file := fs.String("config", "", "path to forge.yaml (default: search upwards)")
	var o buildOptions
	registerBuildFlags(fs, &o)
	rel := fs.String("release", "", "deploy an already packaged release instead of building a new one")
	skipTransfer := fs.Bool("skip-transfer", false, "the release is already staged on the host (after a failed deploy)")
	service := fs.String("service", "", "deploy only this deltactl service (s0, s1...)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	path, cfg, code := a.loadValidConfig("deploy", *file)
	if code != ExitOK {
		return code
	}
	projectDir := filepath.Dir(path)
	cfg = onlyService(cfg, *service)
	if len(cfg.Build.Images) == 0 {
		fmt.Fprintf(a.Stderr, "forge deploy: --service %q matches no image in %s\n", *service, path)
		return ExitUsage
	}
	if *rel != "" {
		if err := release.Validate(*rel); err != nil {
			fmt.Fprintln(a.Stderr, "forge deploy:", err)
			return ExitUsage
		}
		if o.tag != "" {
			fmt.Fprintln(a.Stderr, "forge deploy: --tag and --release cannot be combined")
			return ExitUsage
		}
	}

	if o.dryRun {
		return a.deployDryRun(ctx, cfg, projectDir, o, *rel)
	}

	// 1-2. Build + package, or reuse a packaged release.
	var dir string
	var meta *artifact.Metadata
	if *rel != "" {
		dir = filepath.Join(projectDir, "dist", *rel)
		m, err := artifact.ReadMetadata(dir)
		if err != nil {
			fmt.Fprintln(a.Stderr, "forge deploy: release is not usable:", err)
			return ExitError
		}
		meta = m
	} else {
		res, g, code := a.buildRelease(ctx, cfg, projectDir, o)
		if code != ExitOK {
			return code
		}
		dir, meta, code = a.packageRelease(ctx, cfg, release.Last{ReleaseID: res.ReleaseID, Commit: g.Commit}, filepath.Join(projectDir, "dist"), o.verbose, o.asJSON)
		if code != ExitOK {
			return code
		}
	}
	if meta.Instance != cfg.Deployment.Instance {
		fmt.Fprintf(a.Stderr, "forge deploy: the release was packaged for instance %q but forge.yaml targets %q\n", meta.Instance, cfg.Deployment.Instance)
		return ExitError
	}
	plan, err := planFrom(meta, cfg, *service)
	if err != nil {
		fmt.Fprintln(a.Stderr, "forge deploy:", err)
		return ExitError
	}

	// 3. Transfer and verify on the host.
	log := logging.New(a.Stderr, o.asJSON, o.verbose, logging.NewRunID())
	client := a.sshClient(cfg, log)
	if *skipTransfer {
		staging, err := transport.StagingDir(cfg.Deployment.RemoteDir, meta.ReleaseID)
		if err != nil {
			fmt.Fprintln(a.Stderr, "forge deploy:", err)
			return ExitError
		}
		plan.StagingDir = staging
	} else {
		tr, err := client.Transfer(ctx, transport.Request{LocalDir: dir, ReleaseID: meta.ReleaseID, RemoteDir: cfg.Deployment.RemoteDir})
		if err != nil {
			fmt.Fprintln(a.Stderr, "forge deploy: transfer:", err)
			return ExitError
		}
		plan.StagingDir = tr.StagingDir
	}

	// 4. deltactl load / check / deploy, with rollback on failure.
	dc, err := remote.New(client, cfg.Deployment.DeltactlPath, cfg.Deployment.Instance)
	if err != nil {
		fmt.Fprintln(a.Stderr, "forge deploy:", err)
		return ExitError
	}
	res, applyErr := deploy.Apply(ctx, dc, log, plan)
	return a.reportDeploy(cfg, res, applyErr, o.asJSON)
}

// onlyService returns cfg restricted to one service's image ("" keeps everything).
func onlyService(cfg *config.Config, service string) *config.Config {
	if service == "" {
		return cfg
	}
	c := *cfg
	c.Build.Images = nil
	for _, im := range cfg.Build.Images {
		if im.Service == service {
			c.Build.Images = append(c.Build.Images, im)
		}
	}
	return &c
}

// planFrom maps the packaged images to deltactl services, in forge.yaml order.
func planFrom(m *artifact.Metadata, cfg *config.Config, service string) (deploy.Plan, error) {
	p := deploy.Plan{ReleaseID: m.ReleaseID}
	byName := map[string]artifact.ImageEntry{}
	for _, im := range m.Images {
		byName[im.Name] = im
	}
	for _, im := range cfg.Build.Images {
		e, ok := byName[im.Name]
		if !ok {
			return p, fmt.Errorf("image %q is in forge.yaml but not in release %s", im.Name, m.ReleaseID)
		}
		if e.Service != im.Service {
			return p, fmt.Errorf("image %q is service %s in the release but %s in forge.yaml", im.Name, e.Service, im.Service)
		}
		p.Images = append(p.Images, deploy.Image{Service: e.Service, File: e.File})
	}
	return p, nil
}

func (a *App) deployDryRun(ctx context.Context, cfg *config.Config, projectDir string, o buildOptions, rel string) int {
	id := rel
	if rel == "" {
		res, _, code := a.buildRelease(ctx, cfg, projectDir, o)
		if code != ExitOK {
			return code
		}
		id = res.ReleaseID
		fmt.Fprintln(a.Stdout, "would package the images into", filepath.Join(projectDir, "dist", id))
	}
	staging, err := transport.StagingDir(cfg.Deployment.RemoteDir, id)
	if err != nil {
		fmt.Fprintln(a.Stderr, "forge deploy:", err)
		return ExitError
	}
	d := cfg.Deployment
	fmt.Fprintf(a.Stdout, "would upload to %s@%s:%s and verify with sha256sum\n", d.User, d.Host, staging)
	plan := deploy.Plan{ReleaseID: id, StagingDir: staging}
	for _, im := range cfg.Build.Images {
		plan.Images = append(plan.Images, deploy.Image{Service: im.Service, File: artifact.ImagesDir + "/" + im.Name + ".tar.gz"})
	}
	for _, c := range deploy.Describe(plan, d.DeltactlPath, d.Instance) {
		fmt.Fprintln(a.Stdout, "would run on host:", c)
	}
	fmt.Fprintln(a.Stdout, "on a failed deploy: deltactl rollback for the services already touched, then confirm their tags")
	return ExitOK
}

func (a *App) reportDeploy(cfg *config.Config, res *deploy.Result, applyErr error, asJSON bool) int {
	if asJSON {
		out := struct {
			*deploy.Result
			Error string `json:"error,omitempty"`
		}{Result: res}
		if applyErr != nil {
			out.Error = applyErr.Error()
		}
		b, _ := json.Marshal(out)
		fmt.Fprintln(a.Stdout, string(b))
	}
	inst := cfg.Deployment.Instance
	switch res.Outcome {
	case deploy.Deployed:
		if !asJSON {
			fmt.Fprintf(a.Stdout, "release %s deployed to instance %s\n", res.ReleaseID, inst)
			if res.Status != "" {
				fmt.Fprintln(a.Stdout, res.Status)
			}
		}
		for _, p := range res.Problems {
			fmt.Fprintln(a.Stderr, "forge deploy: warning:", p)
		}
		return ExitOK
	case deploy.Aborted:
		fmt.Fprintf(a.Stderr, "forge deploy: aborted at %s; nothing was deployed and the previous tags are back: %v\n", res.Failed, applyErr)
	case deploy.RolledBack:
		fmt.Fprintf(a.Stderr, "forge deploy: %s failed; instance %s is back on the previous version: %v\n", res.Failed, inst, applyErr)
	case deploy.RollbackFailed:
		fmt.Fprintf(a.Stderr, "forge deploy: %s failed and the previous state of instance %s could NOT be confirmed: %v\n", res.Failed, inst, applyErr)
	}
	for _, p := range res.Problems {
		fmt.Fprintln(a.Stderr, "  -", p)
	}
	if res.Outcome == deploy.RollbackFailed {
		fmt.Fprintf(a.Stderr, "  check the host: deltactl status %s, deltactl runs list --instance %s (a run marked interrupted blocks new operations until `runs resolve`)\n", inst, inst)
	}
	switch res.Outcome {
	case deploy.RolledBack:
		return ExitRolledBack
	case deploy.RollbackFailed:
		return ExitRollbackFailed
	}
	return ExitError
}
