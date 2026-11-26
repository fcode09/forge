package transport

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fcode09/forge/internal/artifact"
	"github.com/fcode09/forge/internal/config"
	"github.com/fcode09/forge/internal/execx"
)

type sshCall struct {
	args  []string // full ssh argv
	stdin string
}

// remote returns the remote command string (last ssh argument).
func (c sshCall) remote() string { return c.args[len(c.args)-1] }

type fakeSSH struct {
	calls  []sshCall
	handle func(remote string) (execx.Result, error)
}

func (f *fakeSSH) Run(ctx context.Context, name string, args ...string) (execx.Result, error) {
	return f.RunStdin(ctx, nil, name, args...)
}

func (f *fakeSSH) RunStdin(_ context.Context, stdin io.Reader, name string, args ...string) (execx.Result, error) {
	if name != "ssh" {
		panic("unexpected program " + name)
	}
	c := sshCall{args: args}
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		c.stdin = string(b)
	}
	f.calls = append(f.calls, c)
	if f.handle != nil {
		return f.handle(c.remote())
	}
	return execx.Result{}, nil
}

func (f *fakeSSH) remotes() []string {
	var out []string
	for _, c := range f.calls {
		out = append(out, c.remote())
	}
	return out
}

func newClient(f *fakeSSH) *Client {
	return &Client{
		Target:  Target{Host: "10.20.0.2", User: "forge-deploy", Port: 22, KnownHosts: "~/.ssh/known_hosts"},
		Exec:    f,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		HomeDir: func() (string, error) { return "/home/u", nil },
	}
}

func TestSSHArgsAreStrict(t *testing.T) {
	f := &fakeSSH{}
	c := newClient(f)
	c.Target.SSHKey = "~/.ssh/forge_deploy"
	if _, err := c.Run(context.Background(), "true"); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(f.calls[0].args, " ")
	for _, want := range []string{
		"-p 22", "-l forge-deploy", "-T", "BatchMode=yes", "StrictHostKeyChecking=yes",
		"UserKnownHostsFile=/home/u/.ssh/known_hosts", "GlobalKnownHostsFile=/dev/null",
		"PasswordAuthentication=no", "ClearAllForwardings=yes", "ForwardAgent=no",
		"-i /home/u/.ssh/forge_deploy", "IdentitiesOnly=yes", "-- 10.20.0.2 true",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "StrictHostKeyChecking=no") || strings.Contains(got, "accept-new") {
		t.Fatal("host key checking was relaxed")
	}
	// The remote command is exactly one argument after "--" and the host.
	a := f.calls[0].args
	if a[len(a)-3] != "--" || a[len(a)-2] != "10.20.0.2" {
		t.Fatalf("args tail=%v", a[len(a)-3:])
	}
}

func TestUnsafeRemoteArgsRefusedBeforeAnythingRuns(t *testing.T) {
	for _, bad := range []string{"a b", "a;b", "$(id)", "`id`", "a|b", "a&b", "a>b", "a\nb", "'x'", `"x"`, "a\\b", "*", "~", "", "a#b", "a!b"} {
		f := &fakeSSH{}
		_, err := newClient(f).Run(context.Background(), "echo", bad)
		if !errors.Is(err, ErrUnsafeArg) || len(f.calls) != 0 {
			t.Errorf("%q: err=%v calls=%d", bad, err, len(f.calls))
		}
	}
	if _, err := RemoteCommand(nil); err == nil {
		t.Fatal("empty command accepted")
	}
}

func TestBadTargetsRefused(t *testing.T) {
	cases := map[string]func(*Client){
		"host starts with dash": func(c *Client) { c.Target.Host = "-oProxyCommand=x" },
		"host with space":       func(c *Client) { c.Target.Host = "a b" },
		"empty user":            func(c *Client) { c.Target.User = "" },
		"bad port":              func(c *Client) { c.Target.Port = 0 },
		"known_hosts space":     func(c *Client) { c.Target.KnownHosts = "/a b/known_hosts" },
		"empty known_hosts":     func(c *Client) { c.Target.KnownHosts = "" },
		"key with space":        func(c *Client) { c.Target.SSHKey = "/a b/key" },
	}
	for name, mut := range cases {
		f := &fakeSSH{}
		c := newClient(f)
		mut(c)
		if _, err := c.Run(context.Background(), "true"); err == nil || len(f.calls) != 0 {
			t.Errorf("%s: err=%v calls=%d", name, err, len(f.calls))
		}
	}
}

// release builds a real, verified artifact directory using the artifact package with a fake docker.
type dockerFake struct{}

func (dockerFake) Run(context.Context, string, ...string) (execx.Result, error) {
	return execx.Result{Stdout: "sha256:" + strings.Repeat("b", 64)}, nil
}

func (dockerFake) Stream(_ context.Context, w io.Writer, _ string, args ...string) (string, error) {
	_, err := io.WriteString(w, strings.Repeat("image-bytes-", 100)+args[len(args)-1])
	return "", err
}

func release(t *testing.T) (string, string) {
	t.Helper()
	cfg, err := config.Parse(strings.NewReader(string(config.Template)))
	if err != nil {
		t.Fatal(err)
	}
	p := &artifact.Packager{Run: dockerFake{}, Stream: dockerFake{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), ForgeVersion: "t"}
	id := "20261125T220000Z-abc1234"
	dir, _, err := p.Package(context.Background(), artifact.Input{Config: cfg, ReleaseID: id, Commit: "abc1234", OutRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return dir, id
}

const dfPlenty = "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/sda1 100000000 1 99999999 1% /srv\n"

func hostOK(remote string) (execx.Result, error) {
	if strings.HasPrefix(remote, "df ") {
		return execx.Result{Stdout: dfPlenty}, nil
	}
	return execx.Result{}, nil
}

func TestTransferHappyPath(t *testing.T) {
	dir, id := release(t)
	f := &fakeSSH{handle: hostOK}
	res, err := newClient(f).Transfer(context.Background(), Request{LocalDir: dir, ReleaseID: id, RemoteDir: "/srv/forge"})
	if err != nil {
		t.Fatal(err)
	}
	staging := "/srv/forge/incoming/" + id
	if res.StagingDir != staging || res.Files != 4 || res.Bytes <= 0 {
		t.Fatalf("res=%+v", res)
	}
	want := []string{
		"mkdir -p /srv/forge/incoming",
		"df -Pk /srv/forge",
		"mkdir -m 0700 " + staging,
		"mkdir -m 0700 " + staging + "/images",
		"dd of=" + staging + "/images/backend.tar.gz bs=1M status=none",
		"dd of=" + staging + "/manifest.json bs=1M status=none",
		"dd of=" + staging + "/metadata.json bs=1M status=none",
		"dd of=" + staging + "/checksums.sha256 bs=1M status=none",
		"env -C " + staging + " sha256sum --check --strict --quiet checksums.sha256",
	}
	got := f.remotes()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("remote commands\n got: %q\nwant: %q", got, want)
	}
	// Uploaded bytes are exactly the local files.
	local, _ := os.ReadFile(filepath.Join(dir, "images", "backend.tar.gz"))
	if f.calls[4].stdin != string(local) {
		t.Fatal("uploaded image bytes differ from the local file")
	}
}

func TestTransferNotEnoughSpaceOrTooBig(t *testing.T) {
	dir, id := release(t)
	f := &fakeSSH{handle: func(remote string) (execx.Result, error) {
		if strings.HasPrefix(remote, "df ") {
			return execx.Result{Stdout: "h\n/dev/x 10 9 1 90% /srv\n"}, nil // 1 KiB free
		}
		return execx.Result{}, nil
	}}
	_, err := newClient(f).Transfer(context.Background(), Request{LocalDir: dir, ReleaseID: id, RemoteDir: "/srv/forge"})
	if err == nil || !strings.Contains(err.Error(), "not enough space") {
		t.Fatalf("err=%v", err)
	}
	for _, r := range f.remotes() {
		if strings.HasPrefix(r, "mkdir -m") || strings.HasPrefix(r, "dd ") {
			t.Fatalf("nothing may be created without room: %q", f.remotes())
		}
	}

	f = &fakeSSH{handle: hostOK}
	_, err = newClient(f).Transfer(context.Background(), Request{LocalDir: dir, ReleaseID: id, RemoteDir: "/srv/forge", MaxBytes: 10})
	if err == nil || !strings.Contains(err.Error(), "over the limit") || len(f.calls) != 0 {
		t.Fatalf("err=%v calls=%d", err, len(f.calls))
	}
}

func TestTransferExistingStagingIsNeverRemoved(t *testing.T) {
	dir, id := release(t)
	f := &fakeSSH{handle: func(remote string) (execx.Result, error) {
		if strings.HasPrefix(remote, "mkdir -m 0700 /srv/forge/incoming/"+id) {
			return execx.Result{Stderr: "mkdir: File exists"}, &execx.ExitError{Name: "ssh", Code: 1}
		}
		return hostOK(remote)
	}}
	_, err := newClient(f).Transfer(context.Background(), Request{LocalDir: dir, ReleaseID: id, RemoteDir: "/srv/forge"})
	if err == nil || !strings.Contains(err.Error(), "already uploaded") {
		t.Fatalf("err=%v", err)
	}
	for _, r := range f.remotes() {
		if strings.HasPrefix(r, "rm ") {
			t.Fatalf("a pre-existing directory must not be removed: %q", f.remotes())
		}
	}
}

func TestTransferFailuresCleanUpTheirOwnStaging(t *testing.T) {
	dir, id := release(t)
	staging := "/srv/forge/incoming/" + id
	failOn := map[string]string{
		"upload fails":          "dd of=" + staging + "/images/backend.tar.gz",
		"remote checksum fails": "env -C " + staging,
	}
	for name, prefix := range failOn {
		f := &fakeSSH{handle: func(remote string) (execx.Result, error) {
			if strings.HasPrefix(remote, prefix) {
				return execx.Result{Stderr: "boom"}, &execx.ExitError{Name: "ssh", Code: 1}
			}
			return hostOK(remote)
		}}
		_, err := newClient(f).Transfer(context.Background(), Request{LocalDir: dir, ReleaseID: id, RemoteDir: "/srv/forge"})
		if err == nil {
			t.Fatalf("%s: expected error", name)
		}
		last := f.remotes()[len(f.remotes())-1]
		if last != "rm -rf -- "+staging {
			t.Errorf("%s: last remote command = %q", name, last)
		}
	}
}

func TestTransferRejectsTamperedLocalArtifact(t *testing.T) {
	dir, id := release(t)
	if err := os.WriteFile(filepath.Join(dir, "images", "backend.tar.gz"), []byte("evil"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeSSH{handle: hostOK}
	if _, err := newClient(f).Transfer(context.Background(), Request{LocalDir: dir, ReleaseID: id, RemoteDir: "/srv/forge"}); err == nil || len(f.calls) != 0 {
		t.Fatalf("err=%v calls=%d", err, len(f.calls))
	}
}

func TestStagingDirValidation(t *testing.T) {
	if got, err := StagingDir("/srv/forge", "v1"); err != nil || got != "/srv/forge/incoming/v1" {
		t.Fatalf("got %q %v", got, err)
	}
	for _, bad := range [][2]string{{"srv/forge", "v1"}, {"/srv/../etc", "v1"}, {"/srv/for ge", "v1"}, {"/srv/forge/", "v1"}, {"/srv/forge", "../x"}, {"/srv/forge", ""}} {
		if _, err := StagingDir(bad[0], bad[1]); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestParseDfAvailable(t *testing.T) {
	if n, err := parseDfAvailable(dfPlenty); err != nil || n != 99999999*1024 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for _, bad := range []string{"", "only one line", "h\nshort row", "h\na b c notanumber d"} {
		if _, err := parseDfAvailable(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
