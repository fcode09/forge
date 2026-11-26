// Package cli is the subcommand dispatcher of the forge binary.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/fcode09/forge/internal/config"
	"github.com/fcode09/forge/internal/execx"
	"github.com/fcode09/forge/internal/version"
)

// Exit codes.
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

// App holds the I/O and outside-world hooks so commands can be tested.
type App struct {
	Stdout, Stderr io.Writer
	Runner         execx.Runner
	LookPath       func(string) (string, error)
	Getwd          func() (string, error)
	Stat           func(string) (os.FileInfo, error)
	HomeDir        func() (string, error)
	// RunnerIn returns a runner that executes in dir and streams its output to stderr.
	RunnerIn func(dir string) execx.Runner
	// Streamer carries large outputs (docker save) to disk without buffering them.
	Streamer execx.Streamer
	Now      func() time.Time
}

// command is one top-level subcommand.
type command struct {
	summary string
	run     func(a *App, ctx context.Context, args []string) int
}

func (a *App) commands() map[string]command {
	return map[string]command{
		"build":   {"Build the declared images and run the tests", (*App).cmdBuild},
		"init":    {"Create a forge.yaml example", (*App).cmdInit},
		"package": {"Export images and write manifest and checksums", (*App).cmdPackage},
		"config":  {"Configuration tools (config validate)", (*App).cmdConfig},
		"doctor":  {"Diagnose Docker, SSH and the configuration", (*App).cmdDoctor},
		"version": {"Show the FORGE version", (*App).cmdVersion},
	}
}

// Run executes args (without the program name) and returns the process exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	cmds := a.commands()
	if len(args) == 0 {
		a.usage(cmds)
		return ExitUsage
	}
	switch args[0] {
	case "help", "-h", "--help":
		a.usage(cmds)
		return ExitOK
	case "-v", "--version":
		return a.cmdVersion(ctx, nil)
	}
	c, ok := cmds[args[0]]
	if !ok {
		fmt.Fprintf(a.Stderr, "forge: unknown command %q\n\n", args[0])
		a.usage(cmds)
		return ExitUsage
	}
	return c.run(a, ctx, args[1:])
}

func (a *App) usage(cmds map[string]command) {
	fmt.Fprintln(a.Stderr, "forge: build, package and ship Docker images, then trigger deltactl on the server")
	fmt.Fprintln(a.Stderr, "\nUsage: forge <command> [flags]\n\nCommands:")
	names := make([]string, 0, len(cmds))
	for n := range cmds {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(a.Stderr, "  %-10s %s\n", n, cmds[n].summary)
	}
}

// newFlags creates a FlagSet that reports errors to stderr and never exits the process.
func (a *App) newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("forge "+name, flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	return fs
}

func (a *App) cmdVersion(_ context.Context, _ []string) int {
	fmt.Fprintln(a.Stdout, "forge", version.Version)
	return ExitOK
}

// loadConfig resolves the config file (--config or search upwards from the working directory) and loads it.
func (a *App) loadConfig(explicit string) (path string, cfg *config.Config, err error) {
	path = explicit
	if path == "" {
		wd, werr := a.Getwd()
		if werr != nil {
			return "", nil, werr
		}
		if path, err = config.FindFile(wd); err != nil {
			return "", nil, err
		}
	}
	cfg, err = config.Load(path)
	return path, cfg, err
}

func joinLines(lines []string) string { return strings.Join(lines, "\n") }

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			if i == 0 {
				return "/"
			}
			return p[:i]
		}
	}
	return "."
}
