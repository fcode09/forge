package cli

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/fcode09/forge/internal/execx"
)

// fakeExec stands in for the ssh binary: it records the remote command (last argument).
type fakeExec struct {
	remotes []string
	fail    string   // remote command prefix that fails
	failIf  []string // remote commands containing any of these fail

	getTag           string // answer to `env ... get` (default 1.0.0)
	afterRollbackTag string // answer to `env ... get` once a rollback ran
	statusOut        string
	stagingList      string // answer to `ls -1t`
	rolled           bool
}

func (f *fakeExec) Run(ctx context.Context, name string, args ...string) (execx.Result, error) {
	return f.RunStdin(ctx, nil, name, args...)
}

func (f *fakeExec) RunStdin(_ context.Context, stdin io.Reader, name string, args ...string) (execx.Result, error) {
	if name != "ssh" {
		panic("unexpected " + name)
	}
	if stdin != nil {
		_, _ = io.Copy(io.Discard, stdin)
	}
	remote := args[len(args)-1]
	f.remotes = append(f.remotes, remote)
	if f.fail != "" && strings.HasPrefix(remote, f.fail) {
		return execx.Result{Stderr: "Host key verification failed."}, &execx.ExitError{Name: "ssh", Code: 255}
	}
	for _, sub := range f.failIf {
		if strings.Contains(remote, sub) {
			return execx.Result{Stderr: "failed: " + sub}, &execx.ExitError{Name: "ssh", Code: 1}
		}
	}
	if strings.Contains(remote, " rollback ") {
		f.rolled = true
	}
	if strings.Contains(remote, " env ") && strings.Contains(remote, " get ") {
		tag := f.getTag
		if tag == "" {
			tag = "1.0.0"
		}
		if f.rolled && f.afterRollbackTag != "" {
			tag = f.afterRollbackTag
		}
		return execx.Result{Stdout: tag + "\n"}, nil
	}
	if strings.HasSuffix(remote, " status colorado") {
		return execx.Result{Stdout: f.statusOut}, nil
	}
	if strings.HasPrefix(remote, "ls ") {
		return execx.Result{Stdout: f.stagingList}, nil
	}
	if strings.HasPrefix(remote, "df ") {
		return execx.Result{Stdout: "h\n/dev/x 9 1 99999999 1% /srv\n"}, nil
	}
	return execx.Result{}, nil
}

func transferApp(t *testing.T, f *fakeExec) (*App, *strings.Builder, *strings.Builder) {
	t.Helper()
	a, out, errb, _ := packageApp(t)
	a.Exec, a.ExecLive = f, f
	return a, out, errb
}

func TestBuildPackageTransfer(t *testing.T) {
	f := &fakeExec{}
	a, out, errb := transferApp(t, f)
	for _, step := range []string{"build", "package", "transfer"} {
		out.Reset()
		if code := run(a, step); code != ExitOK {
			t.Fatalf("%s: code=%d err=%s", step, code, errb.String())
		}
	}
	if !strings.Contains(out.String(), "uploaded to 10.20.0.2:/srv/forge/incoming/20261125T220000Z-abc1234") ||
		!strings.Contains(out.String(), "verified with sha256sum") {
		t.Fatalf("out=%q", out.String())
	}
	last := f.remotes[len(f.remotes)-1]
	if !strings.HasPrefix(last, "env -C /srv/forge/incoming/20261125T220000Z-abc1234 sha256sum --check") {
		t.Fatalf("last remote=%q", last)
	}
}

func TestTransferJSONAndFailures(t *testing.T) {
	f := &fakeExec{}
	a, out, errb := transferApp(t, f)
	run(a, "build")
	run(a, "package")
	out.Reset()
	if code := run(a, "transfer", "--json"); code != ExitOK {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	var res struct {
		ReleaseID  string `json:"release_id"`
		StagingDir string `json:"staging_dir"`
		Files      int    `json:"files"`
	}
	if err := json.Unmarshal([]byte(out.String()), &res); err != nil || res.Files != 4 || !strings.HasSuffix(res.StagingDir, res.ReleaseID) {
		t.Fatalf("out=%q err=%v", out.String(), err)
	}

	f2 := &fakeExec{fail: "mkdir -p"}
	a, _, errb = transferApp(t, f2)
	run(a, "build")
	run(a, "package")
	if code := run(a, "transfer"); code != ExitError || !strings.Contains(errb.String(), "Host key verification failed") {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
}

func TestTransferNeedsPackageAndValidRelease(t *testing.T) {
	a, _, errb := transferApp(t, &fakeExec{})
	if code := run(a, "transfer"); code != ExitError || !strings.Contains(errb.String(), "no previous build") {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
	if code := run(a, "transfer", "--release", "latest"); code != ExitUsage {
		t.Fatalf("latest: %d", code)
	}
	errb.Reset()
	run(a, "build")
	if code := run(a, "transfer"); code != ExitError || !strings.Contains(errb.String(), "local artifact is not valid") {
		t.Fatalf("not packaged: code=%d err=%q", code, errb.String())
	}
}

func TestDoctorRemote(t *testing.T) {
	f := &fakeExec{}
	a, out, _ := transferApp(t, f)
	if code := run(a, "doctor", "--remote"); code != ExitOK || !strings.Contains(out.String(), "deltactl") || !strings.Contains(out.String(), "ssh-connect") {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
	if len(f.remotes) == 0 {
		t.Fatal("--remote must connect")
	}

	// Without --remote, doctor never touches the network.
	f = &fakeExec{}
	a, _, _ = transferApp(t, f)
	run(a, "doctor")
	if len(f.remotes) != 0 {
		t.Fatalf("plain doctor connected: %v", f.remotes)
	}

	f = &fakeExec{fail: "true"}
	a, out, _ = transferApp(t, f)
	if code := run(a, "doctor", "--remote"); code != ExitError || !strings.Contains(out.String(), "Host key verification failed") {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
}
