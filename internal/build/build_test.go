package build

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fcode09/forge/internal/config"
	"github.com/fcode09/forge/internal/execx"
)

type call struct {
	dir  string
	argv []string
}

type fakeRunner struct {
	dir   string
	calls *[]call
	fail  func(argv []string) (execx.Result, error)
}

func (f fakeRunner) Run(_ context.Context, name string, args ...string) (execx.Result, error) {
	argv := append([]string{name}, args...)
	*f.calls = append(*f.calls, call{f.dir, argv})
	if f.fail != nil {
		return f.fail(argv)
	}
	return execx.Result{}, nil
}

func setup(t *testing.T, fail func([]string) (execx.Result, error)) (*Builder, *[]call, *bytes.Buffer, string, *config.Config) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(strings.NewReader(string(config.Template)))
	if err != nil {
		t.Fatal(err)
	}
	var calls []call
	var out bytes.Buffer
	b := &Builder{
		RunnerIn: func(dir string) execx.Runner { return fakeRunner{dir, &calls, fail} },
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Out:      &out,
	}
	return b, &calls, &out, root, cfg
}

func TestRunBuildsThenTests(t *testing.T) {
	b, calls, _, root, cfg := setup(t, nil)
	cfg.Build.Images[0].Target = "production" // the template leaves the stage unset: --target must appear only when asked for
	res, err := b.Run(context.Background(), cfg, Options{ProjectDir: root, ReleaseID: "20261125T000000Z-abc1234", Commit: "abc1234"})
	if err != nil {
		t.Fatal(err)
	}
	if res.TestsRun != 1 || len(res.Images) != 1 || res.Images[0].Ref != "auth-backend:20261125T000000Z-abc1234" {
		t.Fatalf("res=%+v", res)
	}
	if len(*calls) != 3 {
		t.Fatalf("calls=%v", *calls)
	}
	if (*calls)[0].argv[1] != "version" {
		t.Fatalf("first call should check the daemon: %v", (*calls)[0].argv)
	}
	realRoot, _ := filepath.EvalSymlinks(root)
	want := []string{"docker", "build", "--file", filepath.Join(realRoot, "Dockerfile"), "--tag", "auth-backend:20261125T000000Z-abc1234",
		"--target", "production", "--label", "io.forge.release=20261125T000000Z-abc1234",
		"--label", "org.opencontainers.image.version=20261125T000000Z-abc1234",
		"--label", "org.opencontainers.image.revision=abc1234", realRoot}
	if strings.Join((*calls)[1].argv, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("build argv\n got %q\nwant %q", (*calls)[1].argv, want)
	}
	if got := (*calls)[2]; got.argv[0] != "npm" || got.dir != root {
		t.Fatalf("test call=%+v", got)
	}
}

func TestSkipTests(t *testing.T) {
	b, calls, _, root, cfg := setup(t, nil)
	res, err := b.Run(context.Background(), cfg, Options{ProjectDir: root, ReleaseID: "r1", SkipTests: true})
	if err != nil || res.TestsRun != 0 || len(*calls) != 2 {
		t.Fatalf("res=%+v err=%v calls=%v", res, err, *calls)
	}
}

func TestDryRunExecutesNothing(t *testing.T) {
	b, calls, out, root, cfg := setup(t, nil)
	res, err := b.Run(context.Background(), cfg, Options{ProjectDir: root, ReleaseID: "r1", DryRun: true})
	if err != nil || len(*calls) != 0 || !res.DryRun || res.TestsRun != 1 {
		t.Fatalf("res=%+v err=%v calls=%v", res, err, *calls)
	}
	if !strings.Contains(out.String(), "would run: docker build") || !strings.Contains(out.String(), "would run: npm test") {
		t.Fatalf("plan=%q", out.String())
	}
}

func TestBuildFailureStopsBeforeTests(t *testing.T) {
	b, calls, _, root, cfg := setup(t, func(argv []string) (execx.Result, error) {
		if argv[1] == "build" {
			return execx.Result{ExitCode: 1, Stderr: "step 3 failed\nboom"}, &execx.ExitError{Name: "docker", Code: 1}
		}
		return execx.Result{}, nil
	})
	_, err := b.Run(context.Background(), cfg, Options{ProjectDir: root, ReleaseID: "r1"})
	var be *Error
	if !errors.As(err, &be) || be.Step != "image backend" || be.Code != 1 || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err=%v", err)
	}
	for _, c := range *calls {
		if c.argv[0] == "npm" {
			t.Fatal("tests ran after a failed build")
		}
	}
}

func TestTestFailure(t *testing.T) {
	b, _, _, root, cfg := setup(t, func(argv []string) (execx.Result, error) {
		if argv[0] == "npm" {
			return execx.Result{ExitCode: 7}, &execx.ExitError{Name: "npm", Code: 7}
		}
		return execx.Result{}, nil
	})
	_, err := b.Run(context.Background(), cfg, Options{ProjectDir: root, ReleaseID: "r1"})
	var be *Error
	if !errors.As(err, &be) || be.Step != "test npm" || be.Code != 7 {
		t.Fatalf("err=%v", err)
	}
}

func TestDockerDaemonDown(t *testing.T) {
	b, _, _, root, cfg := setup(t, func(argv []string) (execx.Result, error) {
		return execx.Result{Stderr: "Cannot connect"}, &execx.ExitError{Name: "docker", Code: 1}
	})
	_, err := b.Run(context.Background(), cfg, Options{ProjectDir: root, ReleaseID: "r1"})
	if err == nil || !strings.Contains(err.Error(), "docker daemon") {
		t.Fatalf("err=%v", err)
	}
}

func TestMissingDockerfileAndEscapingSymlink(t *testing.T) {
	b, calls, _, root, cfg := setup(t, nil)
	cfg.Build.Images[0].Dockerfile = "missing/Dockerfile"
	if _, err := b.Run(context.Background(), cfg, Options{ProjectDir: root, ReleaseID: "r1", DryRun: true}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err=%v", err)
	}

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "Dockerfile"), []byte("FROM scratch"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	cfg.Build.Images[0].Dockerfile = "link/Dockerfile"
	_, err := b.Run(context.Background(), cfg, Options{ProjectDir: root, ReleaseID: "r1", DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "outside the project") {
		t.Fatalf("symlink escape not rejected: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatal("nothing should have run")
	}
}

func TestInvalidConfigRejected(t *testing.T) {
	b, _, _, root, cfg := setup(t, nil)
	cfg.Deployment.Host = ""
	if _, err := b.Run(context.Background(), cfg, Options{ProjectDir: root, ReleaseID: "r1"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestQuote(t *testing.T) {
	if quote("a b") != "'a b'" || quote("plain-1.0") != "plain-1.0" || quote("it's") != `'it'\''s'` || quote("") != "''" {
		t.Fatal("bad quoting")
	}
}

func TestNoTargetFlagWhenStageIsUnset(t *testing.T) {
	_, _, _, root, cfg := setup(t, nil)
	cfg.Build.Images[0].Target = ""
	argv, err := buildArgs(Options{ProjectDir: root, ReleaseID: "r1"}, cfg.Build.Images[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range argv {
		if a == "--target" {
			t.Fatalf("--target must not be passed when image.target is empty: %v", argv)
		}
	}
}
