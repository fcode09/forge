package cli

import (
	"context"
	"encoding/json"
	"fmt"
)

func (a *App) cmdConfig(_ context.Context, args []string) int {
	if len(args) == 0 || args[0] != "validate" {
		fmt.Fprintln(a.Stderr, "usage: forge config validate [--config FILE] [--json]")
		return ExitUsage
	}
	fs := a.newFlags("config validate")
	file := fs.String("config", "", "path to forge.yaml (default: search upwards)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args[1:]); err != nil {
		return ExitUsage
	}
	path, cfg, err := a.loadConfig(*file)
	var problems []string
	if err != nil {
		problems = []string{err.Error()}
	} else {
		problems = cfg.Validate()
	}
	valid := len(problems) == 0
	if *asJSON {
		out := struct {
			Valid  bool     `json:"valid"`
			Path   string   `json:"path,omitempty"`
			Errors []string `json:"errors"`
		}{valid, path, append([]string{}, problems...)}
		b, _ := json.Marshal(out)
		fmt.Fprintln(a.Stdout, string(b))
	} else if valid {
		fmt.Fprintf(a.Stdout, "%s: valid\n", path)
	} else {
		label := path
		if label == "" {
			label = "forge config validate"
		}
		fmt.Fprintf(a.Stderr, "%s: invalid\n%s\n", label, joinLines(problems))
	}
	if !valid {
		return ExitError
	}
	return ExitOK
}
