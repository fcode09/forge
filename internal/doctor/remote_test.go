package doctor

import (
	"context"
	"strings"
	"testing"

	"github.com/fcode09/forge/internal/execx"
)

type fakeRemote struct {
	fail map[string]string // command prefix -> stderr
	ran  []string
}

func (f *fakeRemote) Run(_ context.Context, argv ...string) (execx.Result, error) {
	cmd := strings.Join(argv, " ")
	f.ran = append(f.ran, cmd)
	for prefix, stderr := range f.fail {
		if strings.HasPrefix(cmd, prefix) {
			return execx.Result{Stderr: stderr}, &execx.ExitError{Name: "ssh", Code: 1}
		}
	}
	switch argv[0] {
	case "df":
		return execx.Result{Stdout: "h\n/dev/x 1000 1 2048 1% /srv\n"}, nil
	case "env":
		return execx.Result{Stdout: "sha256sum (GNU coreutils) 9.4\n"}, nil
	}
	return execx.Result{}, nil
}

func TestRunRemoteAllGood(t *testing.T) {
	f := &fakeRemote{}
	cs := RunRemote(context.Background(), f, goodConfig(t).Deployment)
	if Failed(cs) {
		t.Fatalf("%+v", cs)
	}
	if got := byName(cs, "remote-space").Detail; got != "2 MiB free in /srv/forge" {
		t.Fatalf("space=%q", got)
	}
	if byName(cs, "remote-sha256").Detail != "sha256sum (GNU coreutils) 9.4" || byName(cs, "deltactl").Status != OK {
		t.Fatalf("%+v", cs)
	}
}

func TestRunRemoteConnectFailureSkipsTheRest(t *testing.T) {
	f := &fakeRemote{fail: map[string]string{"true": "Host key verification failed.\nmore"}}
	cs := RunRemote(context.Background(), f, goodConfig(t).Deployment)
	if len(cs) != 1 || cs[0].Status != Fail || !strings.Contains(cs[0].Detail, "Host key verification failed.") || len(f.ran) != 1 {
		t.Fatalf("cs=%+v ran=%v", cs, f.ran)
	}
}

func TestRunRemoteIndividualFailures(t *testing.T) {
	for name, prefix := range map[string]string{
		"remote-dir":    "test -w",
		"remote-sha256": "env -C",
		"deltactl":      "test -x",
	} {
		f := &fakeRemote{fail: map[string]string{prefix: "nope"}}
		cs := RunRemote(context.Background(), f, goodConfig(t).Deployment)
		if byName(cs, name).Status != Fail {
			t.Errorf("%s: %+v", name, cs)
		}
	}
	f := &fakeRemote{fail: map[string]string{"df": "x"}}
	if c := byName(RunRemote(context.Background(), f, goodConfig(t).Deployment), "remote-space"); c.Status != Warn {
		t.Fatalf("%+v", c)
	}
}
