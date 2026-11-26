package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fcode09/forge/internal/config"
)

func (a *App) cmdInit(_ context.Context, args []string) int {
	fs := a.newFlags("init")
	force := fs.Bool("force", false, "overwrite an existing forge.yaml")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	wd, err := a.Getwd()
	if err != nil {
		fmt.Fprintln(a.Stderr, "forge init:", err)
		return ExitError
	}
	target := filepath.Join(wd, config.FileName)
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if *force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(target, flags, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			fmt.Fprintf(a.Stderr, "forge init: %s already exists (use --force to overwrite)\n", target)
		} else {
			fmt.Fprintln(a.Stderr, "forge init:", err)
		}
		return ExitError
	}
	if _, err := f.Write(config.Template); err != nil {
		_ = f.Close()
		fmt.Fprintln(a.Stderr, "forge init:", err)
		return ExitError
	}
	if err := f.Close(); err != nil {
		fmt.Fprintln(a.Stderr, "forge init:", err)
		return ExitError
	}
	fmt.Fprintf(a.Stdout, "created %s: edit it, then run `forge config validate`\n", target)
	return ExitOK
}
