package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fcode09/forge/internal/config"
	"github.com/fcode09/forge/internal/execx"
)

type okRunner struct{}

func (okRunner) Run(_ context.Context, name string, _ ...string) (execx.Result, error) {
	if name == "git" {
		return execx.Result{Stdout: "true\n"}, nil
	}
	return execx.Result{Stdout: "29.9.3\n"}, nil
}

func newApp(t *testing.T, dir string) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, errb bytes.Buffer
	return &App{
		Stdout: &out, Stderr: &errb,
		Runner:   okRunner{},
		LookPath: func(n string) (string, error) { return "/usr/bin/" + n, nil },
		Getwd:    func() (string, error) { return dir, nil },
		Stat:     func(string) (os.FileInfo, error) { return nil, nil },
		HomeDir:  func() (string, error) { return "/home/u", nil },
		Now:      func() time.Time { return time.Date(2026, 11, 25, 22, 0, 0, 0, time.UTC) },
		RunnerIn: func(string) execx.Runner { return okRunner{} },
	}, &out, &errb
}

func run(a *App, args ...string) int { return a.Run(context.Background(), args) }

func TestVersionAndUsage(t *testing.T) {
	a, out, errb := newApp(t, t.TempDir())
	if code := run(a, "version"); code != ExitOK || !strings.HasPrefix(out.String(), "forge ") {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
	if code := run(a); code != ExitUsage {
		t.Fatalf("no args: %d", code)
	}
	errb.Reset()
	if code := run(a, "nope"); code != ExitUsage || !strings.Contains(errb.String(), `unknown command "nope"`) {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
	if code := run(a, "help"); code != ExitOK {
		t.Fatalf("help: %d", code)
	}
}

func TestInit(t *testing.T) {
	dir := t.TempDir()
	a, out, errb := newApp(t, dir)
	if code := run(a, "init"); code != ExitOK || !strings.Contains(out.String(), "created") {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), errb.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, config.FileName))
	if err != nil || !bytes.Equal(got, config.Template) {
		t.Fatalf("file not written correctly: %v", err)
	}
	errb.Reset()
	if code := run(a, "init"); code != ExitError || !strings.Contains(errb.String(), "already exists") {
		t.Fatalf("second init: code=%d err=%q", code, errb.String())
	}
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := run(a, "init", "--force"); code != ExitOK {
		t.Fatalf("force: %d", code)
	}
	got, _ = os.ReadFile(filepath.Join(dir, config.FileName))
	if !bytes.Equal(got, config.Template) {
		t.Fatal("--force did not overwrite")
	}
}

func TestConfigValidate(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "deep", "er")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	a, out, errb := newApp(t, sub)

	if code := run(a, "config", "validate"); code != ExitError || !strings.Contains(errb.String(), "not found") {
		t.Fatalf("missing: code=%d err=%q", code, errb.String())
	}
	if err := os.WriteFile(filepath.Join(dir, config.FileName), config.Template, 0o644); err != nil {
		t.Fatal(err)
	}
	// Works from a subdirectory (M1 exit criterion).
	out.Reset()
	if code := run(a, "config", "validate"); code != ExitOK || !strings.Contains(out.String(), "valid") {
		t.Fatalf("valid: code=%d out=%q", code, out.String())
	}
	out.Reset()
	if code := run(a, "config", "validate", "--json"); code != ExitOK {
		t.Fatalf("json: %d", code)
	}
	var res struct {
		Valid  bool     `json:"valid"`
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || !res.Valid || len(res.Errors) != 0 {
		t.Fatalf("json=%s err=%v", out.String(), err)
	}

	bad := strings.Replace(string(config.Template), "instance: colorado", "instance: ''", 1)
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	errb.Reset()
	if code := run(a, "config", "validate"); code != ExitError || !strings.Contains(errb.String(), "deployment.instance") {
		t.Fatalf("invalid: code=%d err=%q", code, errb.String())
	}
	if code := run(a, "config"); code != ExitUsage {
		t.Fatalf("config without subcommand: %d", code)
	}
	if code := run(a, "config", "validate", "--bogus"); code != ExitUsage {
		t.Fatalf("bad flag: %d", code)
	}
}

func TestDoctor(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.FileName), config.Template, 0o644); err != nil {
		t.Fatal(err)
	}
	a, out, _ := newApp(t, dir)
	if code := run(a, "doctor"); code != ExitOK || !strings.Contains(out.String(), "docker-daemon") {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
	out.Reset()
	if code := run(a, "doctor", "--json"); code != ExitOK {
		t.Fatalf("json: %d", code)
	}
	var res struct {
		OK     bool `json:"ok"`
		Checks []struct{ Name, Status string }
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || !res.OK || len(res.Checks) < 5 {
		t.Fatalf("json=%s err=%v", out.String(), err)
	}

	a.LookPath = func(string) (string, error) { return "", errors.New("missing") }
	if code := run(a, "doctor"); code != ExitError {
		t.Fatalf("missing tools should fail: %d", code)
	}
}
