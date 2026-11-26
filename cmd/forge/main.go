// Command forge builds, packages and ships Docker images and triggers deltactl on the server.
package main

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"time"

	"github.com/fcode09/forge/internal/cli"
	"github.com/fcode09/forge/internal/execx"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	app := &cli.App{
		Stdout: os.Stdout, Stderr: os.Stderr,
		Runner:      execx.Exec{},
		LookPath:    exec.LookPath,
		Getwd:       os.Getwd,
		Stat:        os.Stat,
		HomeDir:     os.UserHomeDir,
		Now:         time.Now,
		Streamer:    execx.Exec{},
		Exec:        execx.Exec{},
		Interactive: execx.Exec{},
		ExecLive:    execx.Exec{Options: execx.Options{Stdout: os.Stderr, Stderr: os.Stderr}},
		RunnerIn: func(dir string) execx.Runner {
			// Build and test output goes to stderr so stdout stays clean for --json.
			return execx.Exec{Options: execx.Options{Dir: dir, Stdout: os.Stderr, Stderr: os.Stderr}}
		},
	}
	os.Exit(app.Run(ctx, os.Args[1:]))
}
