package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fcode09/forge/internal/logging"
	"github.com/fcode09/forge/internal/release"
	"github.com/fcode09/forge/internal/remote"
)

func (a *App) cmdReleases(_ context.Context, args []string) int {
	fs := a.newFlags("releases")
	file := fs.String("config", "", "path to forge.yaml (default: search upwards)")
	n := fs.Int("n", 20, "how many events to show")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	path, _, code := a.loadValidConfig("releases", *file)
	if code != ExitOK {
		return code
	}
	evs, err := release.ReadEvents(filepath.Dir(path))
	if err != nil {
		fmt.Fprintln(a.Stderr, "forge releases:", err)
		return ExitError
	}
	// newest first
	for i, j := 0, len(evs)-1; i < j; i, j = i+1, j-1 {
		evs[i], evs[j] = evs[j], evs[i]
	}
	if *n > 0 && len(evs) > *n {
		evs = evs[:*n]
	}
	if *asJSON {
		if evs == nil {
			evs = []release.Event{}
		}
		b, _ := json.Marshal(evs)
		fmt.Fprintln(a.Stdout, string(b))
		return ExitOK
	}
	if len(evs) == 0 {
		fmt.Fprintln(a.Stdout, "no deploys, rollbacks or uploads recorded yet")
		return ExitOK
	}
	for _, e := range evs {
		rel := e.ReleaseID
		if rel == "" {
			rel = "-"
		}
		who := e.Instance // the deltactl instance, or the upload target of a "ship"
		if who == "" {
			who = e.Target
		}
		fmt.Fprintf(a.Stdout, "%s  %-8s %-26s %-12s %-15s %s\n", e.TimeUTC, e.Action, rel, who, e.Outcome, strings.Join(e.Services, ","))
	}
	return ExitOK
}

func (a *App) cmdStatus(ctx context.Context, args []string) int {
	fs := a.newFlags("status")
	file := fs.String("config", "", "path to forge.yaml (default: search upwards)")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	path, cfg, code := a.loadValidConfig("status", *file)
	if code != ExitOK {
		return code
	}
	if !a.needDeployment("status", cfg) {
		return ExitError
	}
	log := logging.New(a.Stderr, false, *verbose, logging.NewRunID())
	dc, err := remote.New(a.sshClient(cfg, log), cfg.Deployment.DeltactlPath, cfg.Deployment.Instance)
	if err != nil {
		fmt.Fprintln(a.Stderr, "forge status:", err)
		return ExitError
	}
	if evs, err := release.ReadEvents(filepath.Dir(path)); err == nil && len(evs) > 0 {
		e := evs[len(evs)-1]
		fmt.Fprintf(a.Stdout, "last local event: %s %s %s (%s)\n\n", e.TimeUTC, e.Action, e.ReleaseID, e.Outcome)
	}
	out, err := dc.Status(ctx)
	if err != nil {
		fmt.Fprintln(a.Stderr, "forge status:", err)
		return ExitError
	}
	fmt.Fprint(a.Stdout, out)
	return ExitOK
}
