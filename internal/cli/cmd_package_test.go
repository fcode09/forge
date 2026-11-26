package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fcode09/forge/internal/release"
)

type fakeStreamer struct{}

func (fakeStreamer) Stream(_ context.Context, w io.Writer, _ string, args ...string) (string, error) {
	_, err := io.WriteString(w, "TAR:"+args[len(args)-1])
	return "", err
}

func packageApp(t *testing.T) (*App, *strings.Builder, *strings.Builder, string) {
	t.Helper()
	a, out, errb, _ := buildApp(t, gitRunner{commit: "abc1234"})
	a.Streamer = fakeStreamer{}
	wd, _ := a.Getwd()
	return a, out, errb, wd
}

func TestBuildThenPackageUsesLastRelease(t *testing.T) {
	a, out, errb, wd := packageApp(t)
	if code := run(a, "build"); code != ExitOK {
		t.Fatalf("build: %d %s", code, errb.String())
	}
	last, err := release.LoadLast(wd)
	if err != nil || last.ReleaseID != "20261125T220000Z-abc1234" {
		t.Fatalf("last=%+v err=%v", last, err)
	}
	out.Reset()
	if code := run(a, "package"); code != ExitOK {
		t.Fatalf("package: %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "packaged in "+filepath.Join(wd, "dist", last.ReleaseID)) {
		t.Fatalf("out=%q", out.String())
	}
	if code := run(a, "package", "--verify", filepath.Join(wd, "dist", last.ReleaseID)); code != ExitOK {
		t.Fatalf("verify: %d", code)
	}
	// Immutable: a second package of the same release fails.
	errb.Reset()
	if code := run(a, "package"); code != ExitError || !strings.Contains(errb.String(), "immutable") {
		t.Fatalf("repeat: %d %q", code, errb.String())
	}
}

func TestBuildDryRunDoesNotRecord(t *testing.T) {
	a, _, _, wd := packageApp(t)
	if code := run(a, "build", "--dry-run"); code != ExitOK {
		t.Fatal(code)
	}
	if _, err := release.LoadLast(wd); err == nil {
		t.Fatal("dry run must not record a build")
	}
}

func TestPackageWithoutBuildAndWithFlags(t *testing.T) {
	a, out, errb, wd := packageApp(t)
	if code := run(a, "package"); code != ExitError || !strings.Contains(errb.String(), "no previous build") {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
	if code := run(a, "package", "--release", "latest"); code != ExitUsage {
		t.Fatalf("latest: %d", code)
	}
	custom := filepath.Join(t.TempDir(), "out")
	if code := run(a, "package", "--release", "v1.0.0", "--out", custom, "--json"); code != ExitOK {
		t.Fatalf("package: %d %s", code, errb.String())
	}
	var res struct {
		Dir      string
		Metadata struct {
			ReleaseID string `json:"release_id"`
		}
	}
	if err := json.Unmarshal([]byte(out.String()), &res); err != nil || res.Dir != filepath.Join(custom, "v1.0.0") || res.Metadata.ReleaseID != "v1.0.0" {
		t.Fatalf("out=%q err=%v", out.String(), err)
	}
	if _, err := os.Stat(filepath.Join(wd, "dist")); err == nil {
		t.Fatal("default dist must not be created when --out is given")
	}
}

func TestPackageVerifyDetectsCorruption(t *testing.T) {
	a, _, errb, _ := packageApp(t)
	out := filepath.Join(t.TempDir(), "o")
	if code := run(a, "package", "--release", "v1", "--out", out); code != ExitOK {
		t.Fatalf("%d %s", code, errb.String())
	}
	if err := os.WriteFile(filepath.Join(out, "v1", "images", "backend.tar.gz"), []byte("evil"), 0o644); err != nil {
		t.Fatal(err)
	}
	errb.Reset()
	if code := run(a, "package", "--verify", filepath.Join(out, "v1")); code != ExitError || !strings.Contains(errb.String(), "mismatch") {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
}
