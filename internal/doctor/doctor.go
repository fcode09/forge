// Package doctor runs the local diagnostics of `forge doctor`. It never changes anything.
package doctor

import (
	"context"
	"os"
	"runtime"
	"strings"

	"github.com/fcode09/forge/internal/config"
	"github.com/fcode09/forge/internal/dockerx"
	"github.com/fcode09/forge/internal/execx"
)

// Status of one check.
type Status string

// Possible statuses.
const (
	OK   Status = "ok"
	Warn Status = "warn"
	Fail Status = "fail"
)

// Check is one diagnostic result.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
}

// Deps are the outside-world hooks, replaceable in tests.
type Deps struct {
	Runner   execx.Runner
	LookPath func(string) (string, error)
	Stat     func(string) (os.FileInfo, error)
	HomeDir  func() (string, error)
	GOOS     string // operating system; empty means runtime.GOOS
}

// Input is what doctor inspects.
type Input struct {
	ProjectDir string
	ConfigPath string
	Config     *config.Config
	ConfigErr  error
}

// Run executes every local check, in a stable order.
func Run(ctx context.Context, d Deps, in Input) []Check {
	var out []Check
	out = append(out, checkConfig(in))
	out = append(out, checkGit(ctx, d, in.ProjectDir)...)
	out = append(out, checkDocker(ctx, d, in.Config)...)
	out = append(out, checkBinary(d, "ssh", Fail))
	if in.Config != nil && in.Config.HasUpload() {
		out = append(out, checkBinary(d, "scp", Fail)) // `forge ship` copies with scp
	}
	if in.Config != nil && in.Config.Deployment.Configured() {
		out = append(out, checkKnownHosts(d, in.Config.Deployment.KnownHosts))
	}
	return out
}

// Failed reports whether any check failed.
func Failed(cs []Check) bool {
	for _, c := range cs {
		if c.Status == Fail {
			return true
		}
	}
	return false
}

func checkConfig(in Input) Check {
	switch {
	case in.ConfigErr != nil:
		return Check{"config", Fail, in.ConfigErr.Error()}
	case in.Config == nil:
		return Check{"config", Fail, "no configuration loaded"}
	}
	if errs := in.Config.Validate(); len(errs) > 0 {
		return Check{"config", Fail, strings.Join(errs, "; ")}
	}
	return Check{"config", OK, in.ConfigPath}
}

func checkBinary(d Deps, name string, missing Status) Check {
	p, err := d.LookPath(name)
	if err != nil {
		return Check{name, missing, name + " not found in PATH"}
	}
	return Check{name, OK, p}
}

func checkGit(ctx context.Context, d Deps, dir string) []Check {
	bin := checkBinary(d, "git", Fail)
	if bin.Status != OK {
		return []Check{bin}
	}
	res, err := d.Runner.Run(ctx, "git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(res.Stdout) != "true" {
		return []Check{bin, {"git-repo", Warn, "project is not inside a Git work tree; the release ID cannot use a commit"}}
	}
	return []Check{bin, {"git-repo", OK, "inside a Git work tree"}}
}

// checkDocker reports the Docker engine FORGE would use: the native CLI or, on Windows, Docker inside WSL.
func checkDocker(ctx context.Context, d Deps, cfg *config.Config) []Check {
	var mode, distro string
	if cfg != nil {
		mode, distro = cfg.Build.Docker, cfg.Build.WSLDistro
	}
	goos := d.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	e, err := dockerx.Detect(ctx, mode, distro, goos, d.Runner)
	if err != nil {
		return []Check{{"docker-daemon", Fail, err.Error()}}
	}
	bin := Check{"docker", OK, "wsl.exe -e docker (" + e.String() + ")"}
	if e.Mode == dockerx.Native {
		bin = checkBinary(d, "docker", Fail)
	}
	return []Check{bin, {"docker-daemon", OK, "server " + e.Version + viaWSL(e)}}
}

func viaWSL(e dockerx.Engine) string {
	if e.Mode == dockerx.WSL {
		return " via " + e.String()
	}
	return ""
}

func checkKnownHosts(d Deps, path string) Check {
	if strings.HasPrefix(path, "~/") {
		home, err := d.HomeDir()
		if err != nil {
			return Check{"known_hosts", Warn, "cannot resolve home directory: " + err.Error()}
		}
		path = home + path[1:]
	}
	if _, err := d.Stat(path); err != nil {
		return Check{"known_hosts", Warn, path + " not found; SSH host verification will refuse unknown hosts"}
	}
	return Check{"known_hosts", OK, path}
}

func firstLine(stderr string, err error) string {
	if s := strings.TrimSpace(stderr); s != "" {
		return strings.SplitN(s, "\n", 2)[0]
	}
	return err.Error()
}
