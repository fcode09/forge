// Package dockerx decides where the Docker CLI runs: directly on this machine, or inside WSL when Docker lives
// only there (the usual Windows setup without Docker Desktop). Commands stay argument vectors; nothing goes
// through a shell.
package dockerx

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/fcode09/forge/internal/execx"
)

// Mode is where the Docker CLI runs.
type Mode string

// Possible modes.
const (
	Native Mode = "native" // `docker` from this machine's PATH
	WSL    Mode = "wsl"    // `wsl.exe -e docker`: Docker installed inside a WSL distribution
)

// Configuration values of build.docker.
const (
	ConfigAuto   = "auto"
	ConfigNative = "native"
	ConfigWSL    = "wsl"
)

// Engine is a decided way of running Docker.
type Engine struct {
	Mode    Mode
	Distro  string // WSL distribution; empty means the default one
	Version string // Docker server version reported by the probe; empty when the engine was not probed
}

// String describes the engine for logs and diagnostics.
func (e Engine) String() string {
	if e.Mode != WSL {
		return "native"
	}
	if e.Distro != "" {
		return "WSL (" + e.Distro + ")"
	}
	return "WSL"
}

var versionArgs = []string{"version", "--format", "{{.Server.Version}}"}

// Valid reports whether s is an accepted value for build.docker (empty means auto).
func Valid(s string) bool {
	return s == "" || s == ConfigAuto || s == ConfigNative || s == ConfigWSL
}

// Detect finds a Docker engine that answers. With mode "auto" (or empty) it tries the native `docker` first and,
// on Windows only, falls back to Docker inside WSL. "native" and "wsl" try only that one. run is used for the
// probe, which is `docker version`.
func Detect(ctx context.Context, mode, distro, goos string, run execx.Runner) (Engine, error) {
	if !Valid(mode) {
		return Engine{}, fmt.Errorf("unknown docker mode %q (use auto, native or wsl)", mode)
	}
	if mode == "" {
		mode = ConfigAuto
	}
	if mode == ConfigWSL && goos != "windows" {
		return Engine{}, fmt.Errorf("build.docker: wsl is only available on Windows")
	}

	var candidates []Engine
	if mode == ConfigAuto || mode == ConfigNative {
		candidates = append(candidates, Engine{Mode: Native})
	}
	if mode == ConfigWSL || (mode == ConfigAuto && goos == "windows") {
		candidates = append(candidates, Engine{Mode: WSL, Distro: distro})
	}

	var reasons []string
	for _, c := range candidates {
		name, args := c.Command("docker", versionArgs)
		res, err := run.Run(ctx, name, args...)
		if err == nil {
			c.Version = strings.TrimSpace(res.Stdout)
			return c, nil
		}
		reasons = append(reasons, fmt.Sprintf("%s: %s", c, reason(res, err)))
	}
	return Engine{}, fmt.Errorf("docker is not available (%s)", strings.Join(reasons, "; "))
}

// Command returns the program and arguments to execute for name. Only `docker` is rewritten, and only in WSL
// mode, where Windows paths in the arguments become their Linux paths.
func (e Engine) Command(name string, args []string) (string, []string) {
	if name != "docker" || e.Mode != WSL {
		return name, args
	}
	out := make([]string, 0, len(args)+4)
	if e.Distro != "" {
		out = append(out, "-d", e.Distro)
	}
	out = append(out, "-e", "docker")
	for _, a := range args {
		out = append(out, ToWSLPath(a))
	}
	return "wsl.exe", out
}

var drivePath = regexp.MustCompile(`^([A-Za-z]):[\\/](.*)$`)

// ToWSLPath converts a Windows path to the path WSL sees: E:\a\b becomes /mnt/e/a/b and
// \\wsl.localhost\<distro>\home\u becomes /home/u. Anything else is returned unchanged.
// It assumes WSL's default mount root, /mnt.
func ToWSLPath(p string) string {
	if m := drivePath.FindStringSubmatch(p); m != nil {
		return "/mnt/" + strings.ToLower(m[1]) + "/" + strings.ReplaceAll(m[2], `\`, "/")
	}
	for _, prefix := range []string{`\\wsl$\`, `\\wsl.localhost\`} {
		if strings.HasPrefix(p, prefix) {
			_, path, _ := strings.Cut(p[len(prefix):], `\`) // drop <distro>
			return "/" + strings.ReplaceAll(path, `\`, "/")
		}
	}
	return p
}

// Runner returns base adapted to the engine; for the native engine it is base itself.
func (e Engine) Runner(base execx.Runner) execx.Runner {
	if e.Mode != WSL || base == nil {
		return base
	}
	return wslRunner{e: e, base: base}
}

// RunnerIn adapts a runner factory the same way.
func (e Engine) RunnerIn(base func(dir string) execx.Runner) func(dir string) execx.Runner {
	if e.Mode != WSL || base == nil {
		return base
	}
	return func(dir string) execx.Runner { return wslRunner{e: e, base: base(dir)} }
}

// Streamer returns base adapted to the engine; for the native engine it is base itself.
func (e Engine) Streamer(base execx.Streamer) execx.Streamer {
	if e.Mode != WSL || base == nil {
		return base
	}
	return wslStreamer{e: e, base: base}
}

type wslRunner struct {
	e    Engine
	base execx.Runner
}

func (r wslRunner) Run(ctx context.Context, name string, args ...string) (execx.Result, error) {
	n, a := r.e.Command(name, args)
	return r.base.Run(ctx, n, a...)
}

type wslStreamer struct {
	e    Engine
	base execx.Streamer
}

func (s wslStreamer) Stream(ctx context.Context, stdout io.Writer, name string, args ...string) (string, error) {
	n, a := s.e.Command(name, args)
	return s.base.Stream(ctx, stdout, n, a...)
}

// reason is the first line of stderr (wsl.exe writes UTF-16, so NULs are dropped) or the error text.
func reason(res execx.Result, err error) string {
	if s := strings.TrimSpace(strings.ReplaceAll(res.Stderr, "\x00", "")); s != "" {
		return strings.SplitN(s, "\n", 2)[0]
	}
	return err.Error()
}
