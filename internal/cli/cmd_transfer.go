package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/fcode09/forge/internal/logging"
	"github.com/fcode09/forge/internal/release"
	"github.com/fcode09/forge/internal/transport"
)

func (a *App) cmdTransfer(ctx context.Context, args []string) int {
	fs := a.newFlags("transfer")
	file := fs.String("config", "", "path to forge.yaml (default: search upwards)")
	rel := fs.String("release", "", "release ID to upload (default: the last `forge build`)")
	dir := fs.String("dir", "", "release directory (default: <project>/dist/<release>)")
	maxBytes := fs.Int64("max-bytes", 0, "refuse artifacts larger than this (default 10 GiB)")
	verbose := fs.Bool("verbose", false, "debug logging")
	asJSON := fs.Bool("json", false, "machine-readable result on stdout")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	path, cfg, err := a.loadConfig(*file)
	if err != nil {
		fmt.Fprintln(a.Stderr, "forge transfer:", err)
		return ExitError
	}
	if errs := cfg.Validate(); len(errs) > 0 {
		fmt.Fprintf(a.Stderr, "forge transfer: %s is invalid\n%s\n", path, joinLines(errs))
		return ExitError
	}
	projectDir := filepath.Dir(path)

	id := *rel
	if id != "" {
		if err := release.Validate(id); err != nil {
			fmt.Fprintln(a.Stderr, "forge transfer:", err)
			return ExitUsage
		}
	} else {
		last, err := release.LoadLast(projectDir)
		if err != nil {
			fmt.Fprintln(a.Stderr, "forge transfer:", err)
			return ExitError
		}
		id = last.ReleaseID
	}
	local := *dir
	if local == "" {
		local = filepath.Join(projectDir, "dist", id)
	}

	log := logging.New(a.Stderr, *asJSON, *verbose, logging.NewRunID())
	res, err := a.sshClient(cfg, log).Transfer(ctx, transport.Request{
		LocalDir: local, ReleaseID: id, RemoteDir: cfg.Deployment.RemoteDir, MaxBytes: *maxBytes,
	})
	if err != nil {
		fmt.Fprintln(a.Stderr, "forge transfer:", err)
		return ExitError
	}
	if *asJSON {
		b, _ := json.Marshal(struct {
			ReleaseID string `json:"release_id"`
			*transport.Result
		}{id, res})
		fmt.Fprintln(a.Stdout, string(b))
		return ExitOK
	}
	fmt.Fprintf(a.Stdout, "release %s uploaded to %s:%s (%d files, %d bytes) and verified with sha256sum\n",
		id, cfg.Deployment.Host, res.StagingDir, res.Files, res.Bytes)
	return ExitOK
}
