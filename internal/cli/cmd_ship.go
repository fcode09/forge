package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fcode09/forge/internal/artifact"
	"github.com/fcode09/forge/internal/config"
	"github.com/fcode09/forge/internal/execx"
	"github.com/fcode09/forge/internal/logging"
	"github.com/fcode09/forge/internal/release"
	"github.com/fcode09/forge/internal/upload"
)

// cmdShip builds the images, packages them and copies the release to the server with one scp. Nothing runs on
// the server: moving the files into place afterwards is up to the person.
func (a *App) cmdShip(ctx context.Context, args []string) int {
	fs := a.newFlags("ship")
	file := fs.String("config", "", "path to forge.yaml (default: search upwards)")
	var o buildOptions
	registerBuildFlags(fs, &o)
	rel := fs.String("release", "", "upload an already packaged release instead of building a new one")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	path, cfg, code := a.loadValidConfig("ship", *file)
	if code != ExitOK {
		return code
	}
	if !cfg.HasUpload() {
		fmt.Fprintf(a.Stderr, "forge ship: %s has no upload section (set upload.target to your ssh alias)\n", path)
		return ExitError
	}
	if *rel != "" {
		if err := release.Validate(*rel); err != nil {
			fmt.Fprintln(a.Stderr, "forge ship:", err)
			return ExitUsage
		}
		if o.tag != "" {
			fmt.Fprintln(a.Stderr, "forge ship: --tag and --release cannot be combined")
			return ExitUsage
		}
	}
	projectDir := filepath.Dir(path)
	distDir := filepath.Join(projectDir, "dist")

	// 1-2. Build + package, or reuse a packaged release.
	id := *rel
	var commit string
	switch {
	case *rel != "":
		if m, err := artifact.ReadMetadata(filepath.Join(distDir, id)); err != nil {
			fmt.Fprintln(a.Stderr, "forge ship: release is not usable:", err)
			return ExitError
		} else {
			commit = m.Commit
		}
	case o.dryRun:
		res, _, code := a.buildRelease(ctx, cfg, projectDir, o)
		if code != ExitOK {
			return code
		}
		id = res.ReleaseID
	default:
		res, g, code := a.buildRelease(ctx, cfg, projectDir, o)
		if code != ExitOK {
			return code
		}
		_, meta, code := a.packageRelease(ctx, cfg, release.Last{ReleaseID: res.ReleaseID, Commit: g.Commit}, distDir, o.verbose, o.asJSON)
		if code != ExitOK {
			return code
		}
		id, commit = meta.ReleaseID, meta.Commit
	}

	req := upload.Request{Target: cfg.Upload.Target, RemoteDir: cfg.Upload.RemoteDir, ReleaseID: id, LocalDir: filepath.Join(distDir, id)}
	if o.dryRun {
		plan, err := upload.Describe(req)
		if err != nil {
			fmt.Fprintln(a.Stderr, "forge ship:", err)
			return ExitError
		}
		out := a.Stdout
		if o.asJSON {
			out = a.Stderr
		}
		fmt.Fprintln(out, "would run:", plan)
		return ExitOK
	}

	// 3. Upload.
	if _, err := os.Stat(req.LocalDir); err != nil {
		fmt.Fprintf(a.Stderr, "forge ship: release %s is not packaged in %s (run `forge package` first)\n", id, distDir)
		return ExitError
	}
	log := logging.New(a.Stderr, o.asJSON, o.verbose, logging.NewRunID())
	var runner execx.Interactive = a.Interactive
	if runner == nil {
		fmt.Fprintln(a.Stderr, "forge ship: no interactive runner configured")
		return ExitError
	}
	res, err := (&upload.Uploader{Run: runner, Log: log}).Upload(ctx, req)
	ev := release.Event{Action: "ship", ReleaseID: id, Commit: commit, Target: cfg.Upload.Target, Outcome: "uploaded"}
	if err != nil {
		ev.Outcome, ev.Error = "failed", err.Error()
	}
	a.recordEvent(projectDir, ev)
	if err != nil {
		fmt.Fprintln(a.Stderr, "forge ship:", err)
		return ExitError
	}
	a.pruneLocal(cfg, distDir, id, log)

	if o.asJSON {
		b, _ := json.Marshal(struct {
			*upload.Result
			Verify string `json:"verify_command"`
		}{res, res.VerifyCommand()})
		fmt.Fprintln(a.Stdout, string(b))
		return ExitOK
	}
	fmt.Fprintf(a.Stdout, "release %s uploaded to %s:%s (%d files, %d bytes)\n", id, res.Target, res.RemoteDir, res.Files, res.Bytes)
	fmt.Fprintln(a.Stdout, "to check the copy on the server:")
	fmt.Fprintf(a.Stdout, "  %s\n", res.VerifyCommand())
	return ExitOK
}

// pruneLocal applies retention.releases to dist/. A failure is only a warning: the upload already succeeded.
func (a *App) pruneLocal(cfg *config.Config, distDir, current string, log interface{ Info(string, ...any) }) {
	removed, err := release.PruneLocal(distDir, cfg.Retention.Releases, current)
	switch {
	case err != nil:
		fmt.Fprintln(a.Stderr, "forge: warning: local retention:", err)
	case len(removed) > 0:
		log.Info("removed old local releases", "releases", removed)
	}
}
