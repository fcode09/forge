// Command forge builds, packages and ships Docker images and triggers deltactl on the server.
package main

import (
	"context"
	"os"
	"os/exec"
	"os/signal"

	"github.com/fcode09/forge/internal/cli"
	"github.com/fcode09/forge/internal/execx"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	app := &cli.App{
		Stdout: os.Stdout, Stderr: os.Stderr,
		Runner:   execx.Exec{},
		LookPath: exec.LookPath,
		Getwd:    os.Getwd,
		Stat:     os.Stat,
		HomeDir:  os.UserHomeDir,
	}
	os.Exit(app.Run(ctx, os.Args[1:]))
}
