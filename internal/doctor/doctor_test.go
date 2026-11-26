package doctor

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/fcode09/forge/internal/config"
	"github.com/fcode09/forge/internal/execx"
)

type fakeRunner struct {
	calls [][]string
	fn    func(name string, args []string) (execx.Result, error)
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (execx.Result, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	return f.fn(name, args)
}

func deps(r execx.Runner, missing ...string) Deps {
	return Deps{
		Runner: r,
		LookPath: func(n string) (string, error) {
			for _, m := range missing {
				if m == n {
					return "", errors.New("not found")
				}
			}
			return "/usr/bin/" + n, nil
		},
		Stat:    func(string) (os.FileInfo, error) { return nil, nil },
		HomeDir: func() (string, error) { return "/home/u", nil },
	}
}

func goodConfig(t *testing.T) *config.Config {
	t.Helper()
	c, err := config.Parse(strings.NewReader(string(config.Template)))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func byName(cs []Check, n string) Check {
	for _, c := range cs {
		if c.Name == n {
			return c
		}
	}
	return Check{}
}

func TestAllGood(t *testing.T) {
	r := &fakeRunner{fn: func(name string, args []string) (execx.Result, error) {
		if name == "git" {
			return execx.Result{Stdout: "true\n"}, nil
		}
		return execx.Result{Stdout: "29.9.3\n"}, nil
	}}
	cs := Run(context.Background(), deps(r), Input{ProjectDir: "/p", ConfigPath: "/p/forge.yaml", Config: goodConfig(t)})
	if Failed(cs) {
		t.Fatalf("unexpected failure: %+v", cs)
	}
	if got := byName(cs, "docker-daemon").Detail; got != "server 29.9.3" {
		t.Fatalf("detail = %q", got)
	}
	if byName(cs, "known_hosts").Status != OK {
		t.Fatalf("known_hosts: %+v", byName(cs, "known_hosts"))
	}
}

func TestMissingToolsAndDaemonDown(t *testing.T) {
	r := &fakeRunner{fn: func(name string, args []string) (execx.Result, error) {
		return execx.Result{Stderr: "Cannot connect\nmore"}, &execx.ExitError{Name: name, Code: 1}
	}}
	cs := Run(context.Background(), deps(r, "ssh"), Input{ProjectDir: "/p", Config: goodConfig(t)})
	if byName(cs, "ssh").Status != Fail || byName(cs, "docker-daemon").Status != Fail || byName(cs, "git-repo").Status != Warn {
		t.Fatalf("%+v", cs)
	}
	if !strings.Contains(byName(cs, "docker-daemon").Detail, "Cannot connect") || strings.Contains(byName(cs, "docker-daemon").Detail, "more") {
		t.Fatalf("detail = %q", byName(cs, "docker-daemon").Detail)
	}
	if !Failed(cs) {
		t.Fatal("expected Failed")
	}
}

func TestConfigErrorsReported(t *testing.T) {
	r := &fakeRunner{fn: func(string, []string) (execx.Result, error) { return execx.Result{Stdout: "true"}, nil }}
	cs := Run(context.Background(), deps(r), Input{ConfigErr: config.ErrNotFound})
	if byName(cs, "config").Status != Fail {
		t.Fatalf("%+v", cs)
	}
	bad := goodConfig(t)
	bad.Deployment.Host = ""
	cs = Run(context.Background(), deps(r), Input{Config: bad})
	if c := byName(cs, "config"); c.Status != Fail || !strings.Contains(c.Detail, "deployment.host") {
		t.Fatalf("%+v", c)
	}
}

func TestKnownHostsMissingIsWarning(t *testing.T) {
	d := deps(&fakeRunner{fn: func(string, []string) (execx.Result, error) { return execx.Result{Stdout: "true"}, nil }})
	d.Stat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	cs := Run(context.Background(), d, Input{Config: goodConfig(t)})
	if c := byName(cs, "known_hosts"); c.Status != Warn || !strings.Contains(c.Detail, "/home/u/.ssh/known_hosts") {
		t.Fatalf("%+v", c)
	}
}

func TestDockerInsideWSLIsReported(t *testing.T) {
	r := &fakeRunner{fn: func(name string, args []string) (execx.Result, error) {
		switch name {
		case "git":
			return execx.Result{Stdout: "true\n"}, nil
		case "docker":
			return execx.Result{}, errors.New("executable file not found")
		}
		return execx.Result{Stdout: "28.1.1\n"}, nil // wsl.exe -e docker version
	}}
	d := deps(r, "docker")
	d.GOOS = "windows"
	cs := Run(context.Background(), d, Input{ProjectDir: "/p", ConfigPath: "/p/forge.yaml", Config: goodConfig(t)})
	if Failed(cs) {
		t.Fatalf("unexpected failure: %+v", cs)
	}
	if got := byName(cs, "docker-daemon").Detail; got != "server 28.1.1 via WSL" {
		t.Fatalf("detail = %q", got)
	}
	if got := byName(cs, "docker").Detail; !strings.Contains(got, "wsl.exe") {
		t.Fatalf("docker detail = %q", got)
	}
}

func TestScpCheckedOnlyWhenUploadIsConfigured(t *testing.T) {
	r := &fakeRunner{fn: func(string, []string) (execx.Result, error) { return execx.Result{Stdout: "true\n"}, nil }}
	with := goodConfig(t) // the template has an upload section
	if c := byName(Run(context.Background(), deps(r, "scp"), Input{ProjectDir: "/p", Config: with}), "scp"); c.Status != Fail {
		t.Fatalf("scp missing must fail when upload is configured: %+v", c)
	}
	without := goodConfig(t)
	without.Upload = config.Upload{}
	if c := byName(Run(context.Background(), deps(r, "scp"), Input{ProjectDir: "/p", Config: without}), "scp"); c.Name != "" {
		t.Fatalf("scp must not be checked without upload: %+v", c)
	}
}
