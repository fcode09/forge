package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/fcode09/forge/internal/deploy"
	"github.com/fcode09/forge/internal/logging"
	"github.com/fcode09/forge/internal/release"
	"github.com/fcode09/forge/internal/remote"
)

func (a *App) cmdRollback(ctx context.Context, args []string) int {
	fs := a.newFlags("rollback")
	file := fs.String("config", "", "path to forge.yaml (default: search upwards)")
	service := fs.String("service", "", "roll back only this deltactl service (s0, s1...)")
	yes := fs.Bool("yes", false, "do it; without it only the current tags are shown")
	verbose := fs.Bool("verbose", false, "debug logging")
	asJSON := fs.Bool("json", false, "machine-readable result on stdout")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	path, cfg, code := a.loadValidConfig("rollback", *file)
	if code != ExitOK {
		return code
	}
	if !a.needDeployment("rollback", cfg) {
		return ExitError
	}
	projectDir := filepath.Dir(path)
	cfg = onlyService(cfg, *service)
	var services []string
	for _, im := range cfg.Build.Images {
		services = append(services, im.Service)
	}
	if len(services) == 0 {
		fmt.Fprintf(a.Stderr, "forge rollback: --service %q matches no image in %s\n", *service, path)
		return ExitUsage
	}

	log := logging.New(a.Stderr, *asJSON, *verbose, logging.NewRunID())
	client := a.sshClient(cfg, log)
	dc, err := remote.New(client, cfg.Deployment.DeltactlPath, cfg.Deployment.Instance)
	if err != nil {
		fmt.Fprintln(a.Stderr, "forge rollback:", err)
		return ExitError
	}
	inst := cfg.Deployment.Instance

	if !*yes {
		fmt.Fprintf(a.Stdout, "instance %s on %s currently uses:\n", inst, cfg.Deployment.Host)
		for _, s := range services {
			tag, err := dc.GetTag(ctx, s)
			if err != nil {
				fmt.Fprintln(a.Stderr, "forge rollback:", err)
				return ExitError
			}
			fmt.Fprintf(a.Stdout, "  %-4s %s\n", s, tag)
		}
		fmt.Fprintln(a.Stdout, "run again with --yes to roll these services back to the tags deltactl saved at the last deploy")
		fmt.Fprintln(a.Stdout, "(a rollback changes the code version only: it does not undo database changes)")
		return ExitUsage
	}

	res, rbErr := deploy.Revert(ctx, dc, log, services)
	ev := release.Event{Action: "rollback", Instance: inst, Services: services, Outcome: string(res.Outcome)}
	if rbErr != nil {
		ev.Error = rbErr.Error()
	}
	a.recordEvent(projectDir, ev)

	if *asJSON {
		out := struct {
			*deploy.Result
			Error string `json:"error,omitempty"`
		}{Result: res}
		if rbErr != nil {
			out.Error = rbErr.Error()
		}
		b, _ := json.Marshal(out)
		fmt.Fprintln(a.Stdout, string(b))
	}
	switch res.Outcome {
	case deploy.RolledBack:
		if !*asJSON {
			fmt.Fprintf(a.Stdout, "instance %s rolled back (%v)\n", inst, services)
		}
		for _, p := range res.Problems {
			fmt.Fprintln(a.Stderr, "forge rollback: warning:", p)
		}
		return ExitOK
	case deploy.RollbackFailed:
		fmt.Fprintf(a.Stderr, "forge rollback: failed for instance %s: %v\n", inst, rbErr)
		for _, p := range res.Problems {
			fmt.Fprintln(a.Stderr, "  -", p)
		}
		fmt.Fprintf(a.Stderr, "  check the host: deltactl status %s, deltactl runs list --instance %s\n", inst, inst)
		return ExitRollbackFailed
	}
	fmt.Fprintln(a.Stderr, "forge rollback:", rbErr)
	return ExitError
}
