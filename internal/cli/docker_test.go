package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/fcode09/forge/internal/execx"
)

// noNativeDocker behaves like a Windows machine whose Docker lives only inside WSL: `docker` does not exist,
// `wsl.exe -e docker ...` works, git answers as usual.
type noNativeDocker struct{ inner gitRunner }

func (n noNativeDocker) Run(ctx context.Context, name string, args ...string) (execx.Result, error) {
	if name == "docker" {
		return execx.Result{}, &execx.ExitError{Name: "docker", Code: 127, Stderr: "not found"}
	}
	return n.inner.Run(ctx, name, args...)
}

func TestBuildFallsBackToDockerInWSL(t *testing.T) {
	a, out, errb, ran := buildApp(t, gitRunner{commit: "abc1234"})
	a.goos = "windows"
	r := noNativeDocker{inner: gitRunner{commit: "abc1234", ran: ran}}
	a.Runner = r
	a.RunnerIn = func(string) execx.Runner { return r }

	if code := run(a, "build"); code != ExitOK {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "tests run: 1") {
		t.Fatalf("out=%q", out.String())
	}
	var sawBuild, sawNative bool
	for _, c := range *ran {
		if c[0] == "docker" {
			sawNative = true
		}
		if c[0] == "wsl.exe" && c[1] == "-e" && c[2] == "docker" && c[3] == "build" {
			sawBuild = true
		}
	}
	if !sawBuild || sawNative {
		t.Fatalf("expected the build to go through wsl.exe only, ran=%v", *ran)
	}
	if !strings.Contains(errb.String(), "WSL") {
		t.Fatalf("the chosen engine should be logged: %q", errb.String())
	}
}

func TestBuildFailsClearlyWithoutAnyDocker(t *testing.T) {
	a, _, errb, ran := buildApp(t, gitRunner{commit: "abc1234"})
	a.goos = "linux"
	r := noNativeDocker{inner: gitRunner{commit: "abc1234", ran: ran}}
	a.Runner = r
	a.RunnerIn = func(string) execx.Runner { return r }

	if code := run(a, "build"); code != ExitError || !strings.Contains(errb.String(), "docker is not available") {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
	for _, c := range *ran {
		if c[0] == "npm" {
			t.Fatal("tests must not run without Docker")
		}
	}
}
