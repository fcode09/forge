package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/fcode09/forge/internal/build"
	"github.com/fcode09/forge/internal/gitmeta"
	"github.com/fcode09/forge/internal/logging"
	"github.com/fcode09/forge/internal/release"
)

func (a *App) cmdBuild(ctx context.Context, args []string) int {
	fs := a.newFlags("build")
	file := fs.String("config", "", "path to forge.yaml (default: search upwards)")
	tag := fs.String("tag", "", "release ID override (default: <UTC timestamp>-<commit>)")
	allowDirty := fs.Bool("allow-dirty", false, "build even with uncommitted changes or outside a Git repository")
	skipTests := fs.Bool("skip-tests", false, "do not run build.tests")
	dryRun := fs.Bool("dry-run", false, "print the commands without running them")
	verbose := fs.Bool("verbose", false, "debug logging")
	asJSON := fs.Bool("json", false, "machine-readable result on stdout")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	path, cfg, err := a.loadConfig(*file)
	if err != nil {
		fmt.Fprintln(a.Stderr, "forge build:", err)
		return ExitError
	}
	if errs := cfg.Validate(); len(errs) > 0 {
		fmt.Fprintf(a.Stderr, "forge build: %s is invalid\n%s\n", path, joinLines(errs))
		return ExitError
	}
	projectDir := filepath.Dir(path)

	g, err := gitmeta.Read(ctx, a.Runner, projectDir)
	if err != nil {
		fmt.Fprintln(a.Stderr, "forge build: reading Git state:", err)
		return ExitError
	}
	switch {
	case !g.IsRepo && !*allowDirty:
		fmt.Fprintln(a.Stderr, "forge build: the project is not in a Git repository with at least one commit (use --allow-dirty to build anyway)")
		return ExitError
	case g.Dirty && !*allowDirty:
		fmt.Fprintln(a.Stderr, "forge build: the work tree has uncommitted changes (commit them, or use --allow-dirty to build a -dirty release)")
		return ExitError
	}

	id := release.NewID(a.Now(), g)
	if *tag != "" {
		if err := release.Validate(*tag); err != nil {
			fmt.Fprintln(a.Stderr, "forge build:", err)
			return ExitUsage
		}
		id = *tag
	}

	log := logging.New(a.Stderr, *asJSON, *verbose, logging.NewRunID())
	b := &build.Builder{RunnerIn: a.RunnerIn, Log: log, Out: a.Stdout, Now: a.Now}
	if *asJSON {
		b.Out = a.Stderr // keep stdout valid JSON
	}
	res, err := b.Run(ctx, cfg, build.Options{
		ProjectDir: projectDir, ReleaseID: id, Commit: g.Commit, SkipTests: *skipTests, DryRun: *dryRun,
	})
	if err != nil {
		fmt.Fprintln(a.Stderr, "forge build:", err)
		return ExitError
	}

	if !*dryRun {
		last := release.Last{ReleaseID: res.ReleaseID, Commit: g.Commit, CreatedUTC: a.Now().UTC().Format(time.RFC3339)}
		if err := release.SaveLast(projectDir, last); err != nil {
			fmt.Fprintln(a.Stderr, "forge build: warning: could not record the build:", err)
		}
	}

	if *asJSON {
		b, _ := json.Marshal(res)
		fmt.Fprintln(a.Stdout, string(b))
		return ExitOK
	}
	fmt.Fprintf(a.Stdout, "release %s\n", res.ReleaseID)
	for _, im := range res.Images {
		fmt.Fprintf(a.Stdout, "  %-8s %s\n", im.Service, im.Ref)
	}
	if !*dryRun {
		fmt.Fprintf(a.Stdout, "tests run: %d\n", res.TestsRun)
	}
	return ExitOK
}
