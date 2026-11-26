// Package doctor runs the local diagnostics of `forge doctor`. It never changes anything.
package doctor

import (
	"context"
	"os"
	"strings"

	"github.com/fcode09/forge/internal/config"
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
	out = append(out, checkDocker(ctx, d)...)
	out = append(out, checkBinary(d, "ssh", Fail), checkBinary(d, "sftp", Fail))
	if in.Config != nil {
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

func checkDocker(ctx context.Context, d Deps) []Check {
	bin := checkBinary(d, "docker", Fail)
	if bin.Status != OK {
		return []Check{bin}
	}
	res, err := d.Runner.Run(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	if err != nil {
		return []Check{bin, {"docker-daemon", Fail, "docker daemon does not answer: " + firstLine(res.Stderr, err)}}
	}
	return []Check{bin, {"docker-daemon", OK, "server " + strings.TrimSpace(res.Stdout)}}
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
