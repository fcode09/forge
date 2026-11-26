package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fcode09/forge/internal/config"
	"github.com/fcode09/forge/internal/execx"
)

// gitRunner answers git questions from fixed values and records everything else.
type gitRunner struct {
	commit, status string
	notRepo        bool
	dockerFails    bool
	ran            *[][]string
}

func (g gitRunner) Run(_ context.Context, name string, args ...string) (execx.Result, error) {
	argv := append([]string{name}, args...)
	switch {
	case name == "git" && args[2] == "rev-parse":
		if g.notRepo {
			return execx.Result{}, &execx.ExitError{Name: "git", Code: 128}
		}
		return execx.Result{Stdout: g.commit + "\n"}, nil
	case name == "git":
		return execx.Result{Stdout: g.status}, nil
	}
	if g.ran != nil {
		*g.ran = append(*g.ran, argv)
	}
	if g.dockerFails && len(args) > 0 && args[0] == "build" {
		return execx.Result{ExitCode: 1, Stderr: "boom"}, &execx.ExitError{Name: "docker", Code: 1}
	}
	return execx.Result{}, nil
}

func project(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, config.FileName), config.Template, 0o644))
	must(os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644))
	return dir
}

func buildApp(t *testing.T, g gitRunner) (*App, *strings.Builder, *strings.Builder, *[][]string) {
	t.Helper()
	dir := project(t)
	a, _, _ := newApp(t, dir)
	var out, errb strings.Builder
	a.Stdout, a.Stderr = &out, &errb
	ran := &[][]string{}
	g.ran = ran
	a.Runner = g
	a.RunnerIn = func(string) execx.Runner { return g }
	return a, &out, &errb, ran
}

func TestBuildSuccess(t *testing.T) {
	a, out, errb, ran := buildApp(t, gitRunner{commit: "abc1234"})
	if code := run(a, "build"); code != ExitOK {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "release 20261125T220000Z-abc1234") || !strings.Contains(out.String(), "tests run: 1") {
		t.Fatalf("out=%q", out.String())
	}
	if len(*ran) != 3 { // docker version, docker build, npm test
		t.Fatalf("ran=%v", *ran)
	}
}

func TestBuildBlocksDirtyAndNoRepo(t *testing.T) {
	a, _, errb, ran := buildApp(t, gitRunner{commit: "abc1234", status: " M x\n"})
	if code := run(a, "build"); code != ExitError || !strings.Contains(errb.String(), "uncommitted") || len(*ran) != 0 {
		t.Fatalf("dirty: code=%d err=%q ran=%v", code, errb.String(), *ran)
	}
	a, out, _, _ := buildApp(t, gitRunner{commit: "abc1234", status: " M x\n"})
	if code := run(a, "build", "--allow-dirty", "--dry-run"); code != ExitOK || !strings.Contains(out.String(), "-dirty") {
		t.Fatalf("allow-dirty: code=%d out=%q", code, out.String())
	}

	a, _, errb, _ = buildApp(t, gitRunner{notRepo: true})
	if code := run(a, "build"); code != ExitError || !strings.Contains(errb.String(), "not in a Git repository") {
		t.Fatalf("norepo: code=%d err=%q", code, errb.String())
	}
	a, out, _, _ = buildApp(t, gitRunner{notRepo: true})
	if code := run(a, "build", "--allow-dirty", "--dry-run"); code != ExitOK || !strings.Contains(out.String(), "-nogit") {
		t.Fatalf("nogit: code=%d out=%q", code, out.String())
	}
}

func TestBuildTagFlag(t *testing.T) {
	a, out, errb, _ := buildApp(t, gitRunner{commit: "abc1234"})
	if code := run(a, "build", "--tag", "latest", "--dry-run"); code != ExitUsage || !strings.Contains(errb.String(), "mutable") {
		t.Fatalf("latest: code=%d err=%q", code, errb.String())
	}
	if code := run(a, "build", "--tag", "v1.0.0", "--dry-run"); code != ExitOK || !strings.Contains(out.String(), "auth-backend:v1.0.0") {
		t.Fatalf("custom: code=%d out=%q", code, out.String())
	}
}

func TestBuildDryRunAndJSON(t *testing.T) {
	a, out, _, ran := buildApp(t, gitRunner{commit: "abc1234"})
	if code := run(a, "build", "--dry-run", "--json"); code != ExitOK || len(*ran) != 0 {
		t.Fatalf("code=%d ran=%v", code, *ran)
	}
	// stdout must be exactly one JSON document: the plan goes to stderr with --json.
	var res struct {
		ReleaseID string `json:"release_id"`
		DryRun    bool   `json:"dry_run"`
		Images    []struct{ Ref string }
	}
	if err := json.Unmarshal([]byte(out.String()), &res); err != nil || !res.DryRun || len(res.Images) != 1 {
		t.Fatalf("out=%q err=%v", out.String(), err)
	}
}

func TestBuildFailure(t *testing.T) {
	a, _, errb, ran := buildApp(t, gitRunner{commit: "abc1234", dockerFails: true})
	if code := run(a, "build"); code != ExitError || !strings.Contains(errb.String(), "image backend failed") {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
	for _, c := range *ran {
		if c[0] == "npm" {
			t.Fatal("tests must not run after a failed build")
		}
	}
}

func TestBuildNeedsConfig(t *testing.T) {
	a, _, _ := newApp(t, t.TempDir())
	var errb strings.Builder
	a.Stderr = &errb
	if code := run(a, "build"); code != ExitError || !strings.Contains(errb.String(), "not found") {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
}
