package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fcode09/forge/internal/doctor"
)

func (a *App) cmdDoctor(ctx context.Context, args []string) int {
	fs := a.newFlags("doctor")
	file := fs.String("config", "", "path to forge.yaml (default: search upwards)")
	asJSON := fs.Bool("json", false, "machine-readable output")
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
