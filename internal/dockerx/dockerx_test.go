package dockerx

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/fcode09/forge/internal/execx"
)

type fakeRun struct {
	calls [][]string
	fn    func(name string, args []string) (execx.Result, error)
}

func (f *fakeRun) Run(_ context.Context, name string, args ...string) (execx.Result, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	return f.fn(name, args)
}

func (f *fakeRun) Stream(_ context.Context, _ io.Writer, name string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	return "", nil
}

func fail(stderr string) (execx.Result, error) {
	return execx.Result{Stderr: stderr}, &execx.ExitError{Name: "x", Code: 1}
}

func TestToWSLPath(t *testing.T) {
	cases := map[string]string{
		`E:\ITC\forge\Dockerfile`:                 "/mnt/e/ITC/forge/Dockerfile",
		`c:/Users/me/app`:                         "/mnt/c/Users/me/app",
		`\\wsl.localhost\Ubuntu\home\u\app`:       "/home/u/app",
		`\\wsl$\Ubuntu\home\u\app`:                "/home/u/app",
		`\\wsl.localhost\Ubuntu`:                  "/",
		`auth-backend:20261125T000000Z-abc1234`:   "auth-backend:20261125T000000Z-abc1234",
		`io.forge.release=20261125T000000Z-abc12`: "io.forge.release=20261125T000000Z-abc12",
		`{{.Server.Version}}`:                     "{{.Server.Version}}",
		`build`:                                   "build",
	}
	for in, want := range cases {
		if got := ToWSLPath(in); got != want {
			t.Errorf("ToWSLPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCommand(t *testing.T) {
	args := []string{"build", "--file", `E:\p\Dockerfile`, "--tag", "app:1", `E:\p`}

	if n, a := (Engine{Mode: Native}).Command("docker", args); n != "docker" || !reflect.DeepEqual(a, args) {
		t.Fatalf("native must not change anything: %s %v", n, a)
	}
	n, a := Engine{Mode: WSL}.Command("docker", args)
	want := []string{"-e", "docker", "build", "--file", "/mnt/e/p/Dockerfile", "--tag", "app:1", "/mnt/e/p"}
	if n != "wsl.exe" || !reflect.DeepEqual(a, want) {
		t.Fatalf("wsl: %s %v", n, a)
	}
	_, a = Engine{Mode: WSL, Distro: "Ubuntu"}.Command("docker", []string{"version"})
	if !reflect.DeepEqual(a, []string{"-d", "Ubuntu", "-e", "docker", "version"}) {
		t.Fatalf("distro: %v", a)
	}
	// Only docker is rewritten: the tests of the project still run on Windows.
	if n, a := (Engine{Mode: WSL}).Command("npm", []string{"test"}); n != "npm" || len(a) != 1 {
		t.Fatalf("npm rewritten: %s %v", n, a)
	}
}

func TestDetectPrefersNative(t *testing.T) {
	r := &fakeRun{fn: func(string, []string) (execx.Result, error) { return execx.Result{Stdout: "29.9.3\n"}, nil }}
	e, err := Detect(context.Background(), "", "", "windows", r)
	if err != nil || e.Mode != Native || e.Version != "29.9.3" || len(r.calls) != 1 {
		t.Fatalf("e=%+v err=%v calls=%v", e, err, r.calls)
	}
}

func TestDetectFallsBackToWSLOnWindows(t *testing.T) {
	r := &fakeRun{fn: func(name string, _ []string) (execx.Result, error) {
		if name == "docker" {
			return execx.Result{}, errors.New(`exec: "docker": executable file not found in %PATH%`)
		}
		return execx.Result{Stdout: "28.1.1\n"}, nil
	}}
	e, err := Detect(context.Background(), "auto", "Ubuntu", "windows", r)
	if err != nil || e.Mode != WSL || e.Distro != "Ubuntu" || e.Version != "28.1.1" {
		t.Fatalf("e=%+v err=%v", e, err)
	}
	want := []string{"wsl.exe", "-d", "Ubuntu", "-e", "docker", "version", "--format", "{{.Server.Version}}"}
	if !reflect.DeepEqual(r.calls[1], want) {
		t.Fatalf("probe = %v", r.calls[1])
	}
}

func TestDetectNeverTriesWSLOffWindows(t *testing.T) {
	r := &fakeRun{fn: func(string, []string) (execx.Result, error) { return fail("Cannot connect") }}
	_, err := Detect(context.Background(), "auto", "", "linux", r)
	if err == nil || !strings.Contains(err.Error(), "Cannot connect") || len(r.calls) != 1 {
		t.Fatalf("err=%v calls=%v", err, r.calls)
	}
	if _, err := Detect(context.Background(), "wsl", "", "linux", r); err == nil || len(r.calls) != 1 {
		t.Fatalf("wsl on linux must be refused before running anything: %v", err)
	}
}

func TestDetectExplicitModesAndErrors(t *testing.T) {
	r := &fakeRun{fn: func(string, []string) (execx.Result, error) { return execx.Result{Stdout: "1\n"}, nil }}
	if e, err := Detect(context.Background(), "wsl", "", "windows", r); err != nil || e.Mode != WSL || r.calls[0][0] != "wsl.exe" {
		t.Fatalf("e=%+v err=%v calls=%v", e, err, r.calls)
	}
	r.calls = nil
	if e, err := Detect(context.Background(), "native", "", "windows", r); err != nil || e.Mode != Native || len(r.calls) != 1 {
		t.Fatalf("e=%+v err=%v calls=%v", e, err, r.calls)
	}
	if _, err := Detect(context.Background(), "podman", "", "windows", r); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestDetectReportsBothReasonsWithoutNULs(t *testing.T) {
	r := &fakeRun{fn: func(name string, _ []string) (execx.Result, error) {
		if name == "wsl.exe" {
			return fail("N\x00o\x00 \x00d\x00i\x00s\x00t\x00r\x00o\x00\nmore")
		}
		return fail("docker: not found")
	}}
	_, err := Detect(context.Background(), "auto", "", "windows", r)
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "native: docker: not found") || !strings.Contains(msg, "WSL: No distro") || strings.ContainsRune(msg, 0) || strings.Contains(msg, "more") {
		t.Fatalf("msg=%q", msg)
	}
}

func TestWrappersRewriteOnlyInWSL(t *testing.T) {
	base := &fakeRun{fn: func(string, []string) (execx.Result, error) { return execx.Result{}, nil }}
	native := Engine{Mode: Native}
	if native.Runner(base) != execx.Runner(base) || native.Streamer(base) != execx.Streamer(base) {
		t.Fatal("native engine must return the base unchanged")
	}

	e := Engine{Mode: WSL}
	_, _ = e.Runner(base).Run(context.Background(), "docker", "image", "inspect", `E:\x`)
	_, _ = e.RunnerIn(func(string) execx.Runner { return base })(`E:\p`).Run(context.Background(), "docker", "version")
	_, _ = e.Streamer(base).Stream(context.Background(), io.Discard, "docker", "save", "app:1")
	want := [][]string{
		{"wsl.exe", "-e", "docker", "image", "inspect", "/mnt/e/x"},
		{"wsl.exe", "-e", "docker", "version"},
		{"wsl.exe", "-e", "docker", "save", "app:1"},
	}
	if !reflect.DeepEqual(base.calls, want) {
		t.Fatalf("calls = %v", base.calls)
	}
}
