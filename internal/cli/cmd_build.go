package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/fcode09/forge/internal/build"
	"github.com/fcode09/forge/internal/config"
	"github.com/fcode09/forge/internal/gitmeta"
	"github.com/fcode09/forge/internal/logging"
	"github.com/fcode09/forge/internal/release"
)

// buildOptions are the flags shared by `forge build` and `forge deploy`.
type buildOptions struct {
	tag        string
	allowDirty bool
	skipTests  bool
	dryRun     bool
	verbose    bool
	asJSON     bool
}

// registerBuildFlags declares the flags shared by build and deploy.
func registerBuildFlags(fs *flag.FlagSet, o *buildOptions) {
	fs.StringVar(&o.tag, "tag", "", "release ID override (default: <UTC timestamp>-<commit>)")
	fs.BoolVar(&o.allowDirty, "allow-dirty", false, "build even with uncommitted changes or outside a Git repository")
	fs.BoolVar(&o.skipTests, "skip-tests", false, "do not run build.tests")
	fs.BoolVar(&o.dryRun, "dry-run", false, "print the commands without running them")
	fs.BoolVar(&o.verbose, "verbose", false, "debug logging")
	fs.BoolVar(&o.asJSON, "json", false, "machine-readable result on stdout")
}

func (a *App) cmdBuild(ctx context.Context, args []string) int {
	fs := a.newFlags("build")
	file := fs.String("config", "", "path to forge.yaml (default: search upwards)")
	var o buildOptions
	registerBuildFlags(fs, &o)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	path, cfg, code := a.loadValidConfig("build", *file)
	if code != ExitOK {
		return code
	}
	res, _, code := a.buildRelease(ctx, cfg, filepath.Dir(path), o)
	if code != ExitOK {
		return code
	}

	if o.asJSON {
		b, _ := json.Marshal(res)
		fmt.Fprintln(a.Stdout, string(b))
		return ExitOK
	}
	fmt.Fprintf(a.Stdout, "release %s\n", res.ReleaseID)
	for _, im := range res.Images {
		fmt.Fprintf(a.Stdout, "  %-8s %s\n", im.Service, im.Ref)
	}
	if !o.dryRun {
		fmt.Fprintf(a.Stdout, "tests run: %d\n", res.TestsRun)
	}
	return ExitOK
}

// loadValidConfig loads and validates forge.yaml, printing the problem and returning an exit code on failure.
func (a *App) loadValidConfig(cmd, explicit string) (string, *config.Config, int) {
	path, cfg, err := a.loadConfig(explicit)
	if err != nil {
		fmt.Fprintf(a.Stderr, "forge %s: %v\n", cmd, err)
		return "", nil, ExitError
	}
	if errs := cfg.Validate(); len(errs) > 0 {
		fmt.Fprintf(a.Stderr, "forge %s: %s is invalid\n%s\n", cmd, path, joinLines(errs))
		return "", nil, ExitError
	}
	return path, cfg, ExitOK
}

// buildRelease checks the Git state, picks the release ID, builds and tests, and records the build.
// On failure it prints the reason and returns a non-zero exit code.
func (a *App) buildRelease(ctx context.Context, cfg *config.Config, projectDir string, o buildOptions) (*build.Result, gitmeta.Info, int) {
	g, err := gitmeta.Read(ctx, a.Runner, projectDir)
	if err != nil {
		fmt.Fprintln(a.Stderr, "forge build: reading Git state:", err)
		return nil, g, ExitError
	}
	allowDirty := o.allowDirty || cfg.Build.AllowDirty
	switch {
	case !g.IsRepo && !allowDirty:
		fmt.Fprintln(a.Stderr, "forge build: the project is not in a Git repository with at least one commit (use --allow-dirty, or set build.allow_dirty: true in forge.yaml, to build anyway)")
		return nil, g, ExitError
	case g.Dirty && !allowDirty:
		fmt.Fprintln(a.Stderr, "forge build: the work tree has uncommitted changes (commit them, use --allow-dirty, or set build.allow_dirty: true in forge.yaml to build a -dirty release)")
		return nil, g, ExitError
	}

	id := release.NewID(a.Now(), g)
	if o.tag != "" {
		if err := release.Validate(o.tag); err != nil {
			fmt.Fprintln(a.Stderr, "forge build:", err)
			return nil, g, ExitUsage
		}
		id = o.tag
	}

	log := logging.New(a.Stderr, o.asJSON, o.verbose, logging.NewRunID())
	if g.Dirty || !g.IsRepo {
		log.Warn("building from a work tree that is not committed: the release ID is marked -dirty or -nogit", "release", id)
	}
	b := &build.Builder{RunnerIn: a.RunnerIn, Log: log, Out: a.Stdout, Now: a.Now}
	if o.asJSON {
		b.Out = a.Stderr // keep stdout valid JSON
	}
	if !o.dryRun {
		engine, err := a.dockerEngine(ctx, cfg, log)
		if err != nil {
			fmt.Fprintln(a.Stderr, "forge build:", err)
			return nil, g, ExitError
		}
		b.RunnerIn = engine.RunnerIn(a.RunnerIn)
	}
	res, err := b.Run(ctx, cfg, build.Options{
		ProjectDir: projectDir, ReleaseID: id, Commit: g.Commit, SkipTests: o.skipTests, DryRun: o.dryRun,
		DaemonChecked: !o.dryRun,
	})
	if err != nil {
		fmt.Fprintln(a.Stderr, "forge build:", err)
		return nil, g, ExitError
	}
	if !o.dryRun {
		last := release.Last{ReleaseID: res.ReleaseID, Commit: g.Commit, CreatedUTC: a.Now().UTC().Format(time.RFC3339)}
		if err := release.SaveLast(projectDir, last); err != nil {
			fmt.Fprintln(a.Stderr, "forge build: warning: could not record the build:", err)
		}
	}
	return res, g, ExitOK
}
