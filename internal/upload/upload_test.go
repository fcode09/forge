package upload

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fcode09/forge/internal/artifact"
	"github.com/fcode09/forge/internal/config"
	"github.com/fcode09/forge/internal/execx"
)

const releaseID = "20261125T220000Z-abc1234"

type fakeDocker struct{}

func (fakeDocker) Run(context.Context, string, ...string) (execx.Result, error) {
	return execx.Result{Stdout: "sha256:" + strings.Repeat("a", 64) + "\n"}, nil
}

func (fakeDocker) Stream(_ context.Context, w io.Writer, _ string, args ...string) (string, error) {
	_, err := io.WriteString(w, "TAR-OF-"+args[len(args)-1])
	return "", err
}

// release packages a real (fake-content) release and returns its directory.
func release(t *testing.T) string {
	t.Helper()
	cfg, err := config.Parse(strings.NewReader(string(config.Template)))
	if err != nil {
		t.Fatal(err)
	}
	p := &artifact.Packager{
		Run: fakeDocker{}, Stream: fakeDocker{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return time.Date(2026, 11, 25, 22, 0, 0, 0, time.UTC) }, ForgeVersion: "test",
	}
	dir, _, err := p.Package(context.Background(), artifact.Input{Config: cfg, ReleaseID: releaseID, Commit: "abc1234", OutRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

type fakeRun struct {
	dir  string
	name string
	args []string
	err  error
	n    int
}

func (f *fakeRun) RunInteractive(_ context.Context, dir, name string, args ...string) error {
	f.n++
	f.dir, f.name, f.args = dir, name, args
	return f.err
}

func uploader(r *fakeRun) *Uploader {
	return &Uploader{Run: r, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func req(dir string) Request {
	return Request{Target: "mpa", RemoteDir: "images", ReleaseID: releaseID, LocalDir: dir}
}

func TestUploadIsOneScpOfTheReleaseFolder(t *testing.T) {
	dir := release(t)
	f := &fakeRun{}
	res, err := uploader(f).Upload(context.Background(), req(dir))
	if err != nil {
		t.Fatal(err)
	}
	if f.n != 1 || f.name != "scp" {
		t.Fatalf("expected exactly one scp, got %d x %q", f.n, f.name)
	}
	// Runs next to the release and names it relatively: "E:\..." would be read by scp as a host.
	if f.dir != filepath.Dir(dir) {
		t.Fatalf("dir=%q want %q", f.dir, filepath.Dir(dir))
	}
	tail := f.args[len(f.args)-3:]
	if tail[0] != "--" || tail[1] != releaseID || tail[2] != "mpa:images/" {
		t.Fatalf("args tail = %v", tail)
	}
	joined := strings.Join(f.args, " ")
	for _, want := range []string{"-r", "StrictHostKeyChecking=yes", "ClearAllForwardings=yes", "ForwardAgent=no"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s in %s", want, joined)
		}
	}
	for _, banned := range []string{"BatchMode", "PasswordAuthentication", "PreferredAuthentications"} {
		if strings.Contains(joined, banned) {
			t.Errorf("%s would block the password prompt: %s", banned, joined)
		}
	}
	if res.RemoteDir != "images/"+releaseID || res.Files != 4 || res.Bytes <= 0 {
		t.Fatalf("res=%+v", res)
	}
	if want := "cd images/" + releaseID + " && sha256sum -c checksums.sha256"; res.VerifyCommand() != want {
		t.Fatalf("verify=%q", res.VerifyCommand())
	}
}

func TestUploadAbsoluteRemoteDir(t *testing.T) {
	f := &fakeRun{}
	r := req(release(t))
	r.Target, r.RemoteDir = "me@10.0.0.5", "/home/me/images"
	res, err := uploader(f).Upload(context.Background(), r)
	if err != nil || f.args[len(f.args)-1] != "me@10.0.0.5:/home/me/images/" || res.RemoteDir != "/home/me/images/"+releaseID {
		t.Fatalf("args=%v res=%+v err=%v", f.args, res, err)
	}
}

func TestUploadRefusesBrokenLocalRelease(t *testing.T) {
	dir := release(t)
	if err := os.WriteFile(filepath.Join(dir, "images", "backend.tar.gz"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeRun{}
	if _, err := uploader(f).Upload(context.Background(), req(dir)); err == nil || !strings.Contains(err.Error(), "not valid") || f.n != 0 {
		t.Fatalf("err=%v scp calls=%d", err, f.n)
	}
}

func TestUploadRejectsUnsafeInput(t *testing.T) {
	dir := release(t)
	cases := map[string]func(*Request){
		"target option":   func(r *Request) { r.Target = "-oProxyCommand=x" },
		"target host:dir": func(r *Request) { r.Target = "host:/etc" },
		"remote dir ..":   func(r *Request) { r.RemoteDir = "../x" },
		"remote dir ~":    func(r *Request) { r.RemoteDir = "~/x" },
		"release id":      func(r *Request) { r.ReleaseID = "../x" },
		"wrong folder":    func(r *Request) { r.LocalDir = filepath.Join(filepath.Dir(dir), "other") },
	}
	for name, mutate := range cases {
		f := &fakeRun{}
		r := req(dir)
		mutate(&r)
		if _, err := uploader(f).Upload(context.Background(), r); err == nil || f.n != 0 {
			t.Errorf("%s: err=%v scp calls=%d", name, err, f.n)
		}
	}
}

func TestScpFailureAndMissingBinary(t *testing.T) {
	dir := release(t)
	f := &fakeRun{err: &execx.ExitError{Name: "scp", Code: 1}}
	_, err := uploader(f).Upload(context.Background(), req(dir))
	if err == nil || !strings.Contains(err.Error(), "exit code 1") || !strings.Contains(err.Error(), "ssh mpa") || !strings.Contains(err.Error(), "partial copy") {
		t.Fatalf("err=%v", err)
	}
	f = &fakeRun{err: os.ErrNotExist}
	if _, err := uploader(f).Upload(context.Background(), req(dir)); err == nil || !strings.Contains(err.Error(), "OpenSSH") {
		t.Fatalf("err=%v", err)
	}
}

func TestDescribe(t *testing.T) {
	dir := release(t)
	s, err := Describe(req(dir))
	if err != nil || !strings.Contains(s, "scp -r") || !strings.HasSuffix(s, "mpa:images/") {
		t.Fatalf("s=%q err=%v", s, err)
	}
}
