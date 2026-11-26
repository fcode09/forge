package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/fcode09/forge/internal/artifact"
	"github.com/fcode09/forge/internal/logging"
	"github.com/fcode09/forge/internal/release"
	"github.com/fcode09/forge/internal/version"
)

func (a *App) cmdPackage(ctx context.Context, args []string) int {
	fs := a.newFlags("package")
	file := fs.String("config", "", "path to forge.yaml (default: search upwards)")
	rel := fs.String("release", "", "release ID to export (default: the last `forge build`)")
	out := fs.String("out", "", "output directory (default: <project>/dist)")
	verify := fs.String("verify", "", "only verify an existing release directory and exit")
	verbose := fs.Bool("verbose", false, "debug logging")
	asJSON := fs.Bool("json", false, "machine-readable result on stdout")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	if *verify != "" {
		if err := artifact.Verify(*verify); err != nil {
			fmt.Fprintln(a.Stderr, "forge package: verification failed:", err)
			return ExitError
		}
		fmt.Fprintf(a.Stdout, "%s: verified\n", *verify)
		return ExitOK
	}

	path, cfg, err := a.loadConfig(*file)
	if err != nil {
		fmt.Fprintln(a.Stderr, "forge package:", err)
		return ExitError
	}
	if errs := cfg.Validate(); len(errs) > 0 {
		fmt.Fprintf(a.Stderr, "forge package: %s is invalid\n%s\n", path, joinLines(errs))
		return ExitError
	}
	projectDir := filepath.Dir(path)

	var last release.Last
	if *rel != "" {
		if err := release.Validate(*rel); err != nil {
			fmt.Fprintln(a.Stderr, "forge package:", err)
			return ExitUsage
		}
		last.ReleaseID = *rel
	} else if last, err = release.LoadLast(projectDir); err != nil {
		fmt.Fprintln(a.Stderr, "forge package:", err)
		return ExitError
	}

	outRoot := *out
	if outRoot == "" {
		outRoot = filepath.Join(projectDir, "dist")
	}
	p := &artifact.Packager{
		Run: a.Runner, Stream: a.Streamer, Now: a.Now, ForgeVersion: version.Version,
		Log: logging.New(a.Stderr, *asJSON, *verbose, logging.NewRunID()),
	}
	dir, meta, err := p.Package(ctx, artifact.Input{Config: cfg, ReleaseID: last.ReleaseID, Commit: last.Commit, OutRoot: outRoot})
	if err != nil {
		fmt.Fprintln(a.Stderr, "forge package:", err)
		return ExitError
	}

	if *asJSON {
		b, _ := json.Marshal(struct {
			Dir      string             `json:"dir"`
			Metadata *artifact.Metadata `json:"metadata"`
		}{dir, meta})
		fmt.Fprintln(a.Stdout, string(b))
		return ExitOK
	}
	fmt.Fprintf(a.Stdout, "release %s packaged in %s\n", meta.ReleaseID, dir)
	for _, im := range meta.Images {
		fmt.Fprintf(a.Stdout, "  %-8s %s  %d bytes  sha256:%s\n", im.Service, im.File, im.Size, im.SHA256)
	}
	return ExitOK
}
