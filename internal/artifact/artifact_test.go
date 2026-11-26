package artifact

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fcode09/forge/internal/config"
	"github.com/fcode09/forge/internal/execx"
)

type fakeDocker struct {
	missing  bool
	saveFail bool
}

func (f fakeDocker) Run(_ context.Context, _ string, args ...string) (execx.Result, error) {
	if f.missing {
		return execx.Result{Stderr: "No such image"}, &execx.ExitError{Name: "docker", Code: 1}
	}
	return execx.Result{Stdout: "sha256:" + strings.Repeat("a", 64) + "\n"}, nil
}

func (f fakeDocker) Stream(_ context.Context, w io.Writer, _ string, args ...string) (string, error) {
	_, _ = io.WriteString(w, "TAR-OF-"+args[len(args)-1])
	if f.saveFail {
		return "disk full", &execx.ExitError{Name: "docker", Code: 1}
	}
	return "", nil
}

func newPackager(f fakeDocker) *Packager {
	return &Packager{
		Run: f, Stream: f, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:          func() time.Time { return time.Date(2026, 11, 25, 22, 0, 0, 0, time.UTC) },
		ForgeVersion: "test",
	}
}

func input(t *testing.T, out string) Input {
	t.Helper()
	cfg, err := config.Parse(strings.NewReader(string(config.Template)))
	if err != nil {
		t.Fatal(err)
	}
	return Input{Config: cfg, ReleaseID: "20261125T220000Z-abc1234", Commit: "abc1234", OutRoot: out}
}

func TestPackageWritesVerifiableRelease(t *testing.T) {
	out := t.TempDir()
	dir, meta, err := newPackager(fakeDocker{}).Package(context.Background(), input(t, out))
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(out, "20261125T220000Z-abc1234") {
		t.Fatalf("dir=%s", dir)
	}
	if meta.Instance != "colorado" || meta.Commit != "abc1234" || meta.CreatedUTC != "2026-11-25T22:00:00Z" || meta.ForgeVersion != "test" {
		t.Fatalf("meta=%+v", meta)
	}
	e := meta.Images[0]
	if e.File != "images/backend.tar.gz" || e.Service != "s0" || e.Ref != "auth-backend:20261125T220000Z-abc1234" || !strings.HasPrefix(e.ImageID, "sha256:") {
		t.Fatalf("entry=%+v", e)
	}

	// The recorded checksum is the SHA-256 of the compressed file, and the file is the gzip of docker save.
	raw, _ := os.ReadFile(filepath.Join(dir, "images", "backend.tar.gz"))
	sum := sha256.Sum256(raw)
	if e.SHA256 != hex.EncodeToString(sum[:]) || e.Size != int64(len(raw)) {
		t.Fatalf("checksum/size mismatch: %+v", e)
	}
	zr, err := gzip.NewReader(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(zr)
	if string(body) != "TAR-OF-auth-backend:20261125T220000Z-abc1234" {
		t.Fatalf("body=%q", body)
	}

	if err := Verify(dir); err != nil {
		t.Fatal(err)
	}
	// Same format the server uses.
	if _, err := exec.LookPath("sha256sum"); err == nil {
		cmd := exec.Command("sha256sum", "-c", ChecksumsFile)
		cmd.Dir = dir
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("sha256sum -c failed: %v\n%s", err, b)
		}
	}
	// No leftovers from the temporary directory.
	entries, _ := os.ReadDir(out)
	if len(entries) != 1 {
		t.Fatalf("out has %d entries", len(entries))
	}
}

func TestReleasesAreImmutable(t *testing.T) {
	out := t.TempDir()
	p := newPackager(fakeDocker{})
	if _, _, err := p.Package(context.Background(), input(t, out)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.Package(context.Background(), input(t, out)); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("err=%v", err)
	}
}

func TestFailuresLeaveNothingBehind(t *testing.T) {
	for name, f := range map[string]fakeDocker{"missing image": {missing: true}, "save fails": {saveFail: true}} {
		out := t.TempDir()
		_, _, err := newPackager(f).Package(context.Background(), input(t, out))
		if err == nil {
			t.Fatalf("%s: expected error", name)
		}
		if entries, _ := os.ReadDir(out); len(entries) != 0 {
			t.Fatalf("%s: left %d entries", name, len(entries))
		}
	}
}

func TestInvalidInputs(t *testing.T) {
	in := input(t, t.TempDir())
	in.ReleaseID = "../evil"
	if _, _, err := newPackager(fakeDocker{}).Package(context.Background(), in); err == nil {
		t.Fatal("bad release id accepted")
	}
	in = input(t, t.TempDir())
	in.Config.Deployment.Host = ""
	if _, _, err := newPackager(fakeDocker{}).Package(context.Background(), in); err == nil {
		t.Fatal("invalid config accepted")
	}
}

func release(t *testing.T) string {
	t.Helper()
	dir, _, err := newPackager(fakeDocker{}).Package(context.Background(), input(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestVerifyDetectsTampering(t *testing.T) {
	cases := map[string]func(dir string){
		"corrupted image": func(dir string) {
			_ = os.WriteFile(filepath.Join(dir, "images", "backend.tar.gz"), []byte("evil"), 0o644)
		},
		"modified metadata": func(dir string) {
			_ = os.WriteFile(filepath.Join(dir, MetadataFile), []byte("{}"), 0o644)
		},
		"extra file": func(dir string) {
			_ = os.WriteFile(filepath.Join(dir, "images", "extra.tar.gz"), []byte("x"), 0o644)
		},
		"missing image":     func(dir string) { _ = os.Remove(filepath.Join(dir, "images", "backend.tar.gz")) },
		"missing checksums": func(dir string) { _ = os.Remove(filepath.Join(dir, ChecksumsFile)) },
		"symlink swapped in": func(dir string) {
			p := filepath.Join(dir, "images", "backend.tar.gz")
			_ = os.Remove(p)
			_ = os.Symlink("/etc/hostname", p)
		},
		"path traversal in checksums": func(dir string) {
			_ = os.WriteFile(filepath.Join(dir, ChecksumsFile), []byte(strings.Repeat("a", 64)+"  ../../etc/passwd\n"), 0o644)
		},
		"absolute path in checksums": func(dir string) {
			_ = os.WriteFile(filepath.Join(dir, ChecksumsFile), []byte(strings.Repeat("a", 64)+"  /etc/passwd\n"), 0o644)
		},
		"malformed checksums": func(dir string) { _ = os.WriteFile(filepath.Join(dir, ChecksumsFile), []byte("nonsense\n"), 0o644) },
		"empty checksums":     func(dir string) { _ = os.WriteFile(filepath.Join(dir, ChecksumsFile), nil, 0o644) },
	}
	for name, tamper := range cases {
		dir := release(t)
		tamper(dir)
		if err := Verify(dir); err == nil {
			t.Errorf("%s: Verify accepted a tampered artifact", name)
		}
	}
}

func TestSafeRelPath(t *testing.T) {
	for _, bad := range []string{"", "/a", "../a", "a/../b", "a//b", "a/./b", "-rf", "a b", "a;b", "a\\b", "a\x00b", "a/"} {
		if SafeRelPath(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, ok := range []string{"images/backend.tar.gz", "manifest.json", "a-b_c.d/e"} {
		if err := SafeRelPath(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
}
