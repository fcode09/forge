// Command forge builds, packages and ships Docker images and triggers deltactl on the server.
package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"time"

	"github.com/fcode09/forge/internal/cli"
	"github.com/fcode09/forge/internal/execx"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Output of child programs (docker build, tests, remote commands) goes to stderr so stdout stays clean for
	// --json. On Windows it is sent with CRLF line ends: Docker inside WSL otherwise prints a staircase.
	var live io.Writer = os.Stderr
	if runtime.GOOS == "windows" {
		live = execx.CRLF(os.Stderr)
	}

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
		ExecLive:    execx.Exec{Options: execx.Options{Stdout: live, Stderr: live}},
		RunnerIn: func(dir string) execx.Runner {
			return execx.Exec{Options: execx.Options{Dir: dir, Stdout: live, Stderr: live}}
		},
	}
	os.Exit(app.Run(ctx, os.Args[1:]))
}
