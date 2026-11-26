package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fcode09/forge/internal/doctor"
	"github.com/fcode09/forge/internal/logging"
)

func (a *App) cmdDoctor(ctx context.Context, args []string) int {
	fs := a.newFlags("doctor")
	file := fs.String("config", "", "path to forge.yaml (default: search upwards)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	remote := fs.Bool("remote", false, "also check the production host over SSH (connects to it)")
	verbose := fs.Bool("verbose", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	path, cfg, cfgErr := a.loadConfig(*file)
	wd, _ := a.Getwd()
	projectDir := wd
	if path != "" {
		projectDir = dirOf(path)
	}
	checks := doctor.Run(ctx, doctor.Deps{
		Runner: a.Runner, LookPath: a.LookPath, Stat: a.Stat, HomeDir: a.HomeDir,
	}, doctor.Input{ProjectDir: projectDir, ConfigPath: path, Config: cfg, ConfigErr: cfgErr})

	if *remote {
		switch {
		case cfg == nil:
			checks = append(checks, doctor.Check{Name: "ssh-connect", Status: doctor.Fail, Detail: "skipped: no valid configuration"})
		case len(cfg.Validate()) > 0:
			checks = append(checks, doctor.Check{Name: "ssh-connect", Status: doctor.Fail, Detail: "skipped: invalid configuration"})
		default:
			log := logging.New(a.Stderr, *asJSON, *verbose, logging.NewRunID())
			checks = append(checks, doctor.RunRemote(ctx, a.sshClient(cfg, log), cfg.Deployment)...)
		}
	}

	if *asJSON {
		b, _ := json.Marshal(struct {
			Checks []doctor.Check `json:"checks"`
			OK     bool           `json:"ok"`
		}{checks, !doctor.Failed(checks)})
		fmt.Fprintln(a.Stdout, string(b))
	} else {
		for _, c := range checks {
			fmt.Fprintf(a.Stdout, "[%-4s] %-14s %s\n", c.Status, c.Name, c.Detail)
		}
	}
	if doctor.Failed(checks) {
		return ExitError
	}
	return ExitOK
}
