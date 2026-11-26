package remote

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fcode09/forge/internal/execx"
)

type fakeHost struct {
	calls []string
	live  []bool
	fn    func(argv []string) (execx.Result, error)
}

func (f *fakeHost) rec(live bool, argv []string) (execx.Result, error) {
	f.calls = append(f.calls, strings.Join(argv, " "))
	f.live = append(f.live, live)
	if f.fn != nil {
		return f.fn(argv)
	}
	return execx.Result{}, nil
}
func (f *fakeHost) Run(_ context.Context, argv ...string) (execx.Result, error) {
	return f.rec(false, argv)
}
func (f *fakeHost) RunLive(_ context.Context, argv ...string) (execx.Result, error) {
	return f.rec(true, argv)
}

func newD(t *testing.T, h Host) *Deltactl {
	t.Helper()
	d, err := New(h, "/srv/itc-deploy/deltactl", "colorado")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCommandsAreTheClosedList(t *testing.T) {
	h := &fakeHost{fn: func(argv []string) (execx.Result, error) {
		if argv[1] == "env" && argv[3] == "get" {
			return execx.Result{Stdout: "1.0.0\n"}, nil
		}
		return execx.Result{}, nil
	}}
	d := newD(t, h)
	ctx := context.Background()
	if tag, err := d.GetTag(ctx, "s0"); err != nil || tag != "1.0.0" {
		t.Fatalf("tag=%q err=%v", tag, err)
	}
	for _, err := range []error{
		d.Load(ctx, "/srv/forge/incoming/r1/images/backend.tar.gz"),
		d.SetTag(ctx, "s0", "r1"),
		d.Check(ctx, "s0"),
		d.Deploy(ctx, "s0"),
		d.Rollback(ctx, "s0"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Status(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/srv/itc-deploy/deltactl env colorado get S0_TAG",
		"/srv/itc-deploy/deltactl load /srv/forge/incoming/r1/images/backend.tar.gz",
		"/srv/itc-deploy/deltactl env colorado set S0_TAG r1",
		"/srv/itc-deploy/deltactl check colorado s0",
		"/srv/itc-deploy/deltactl deploy colorado s0",
		"/srv/itc-deploy/deltactl rollback colorado s0 --yes",
		"/srv/itc-deploy/deltactl status colorado",
	}
	if strings.Join(h.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(h.calls, "\n"), strings.Join(want, "\n"))
	}
	// Long operations stream; reads and writes of a single value are captured.
	wantLive := []bool{false, true, false, true, true, true, false}
	for i, l := range wantLive {
		if h.live[i] != l {
			t.Errorf("call %d (%s): live=%v want %v", i, h.calls[i], h.live[i], l)
		}
	}
}

func TestValidation(t *testing.T) {
	h := &fakeHost{}
	d := newD(t, h)
	ctx := context.Background()
	for name, err := range map[string]error{
		"service s0;id":  d.Deploy(ctx, "s0;id"),
		"service empty":  d.Check(ctx, ""),
		"service web":    d.Rollback(ctx, "web"),
		"tag latest":     d.SetTag(ctx, "s0", "latest"),
		"tag with space": d.SetTag(ctx, "s0", "a b"),
		"tag path":       d.SetTag(ctx, "s0", "../x"),
		"set bad svc":    d.SetTag(ctx, "S0", "v1"),
	} {
		if err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := d.GetTag(ctx, "x"); err == nil {
		t.Error("bad service accepted by GetTag")
	}
	if len(h.calls) != 0 {
		t.Fatalf("nothing may reach the host: %v", h.calls)
	}
	for _, bad := range [][2]string{{"relative/deltactl", "colorado"}, {"/a/../b", "colorado"}, {"/srv/deltactl", "Co lorado"}, {"/srv/deltactl", ""}} {
		if _, err := New(h, bad[0], bad[1]); err == nil {
			t.Errorf("New%v accepted", bad)
		}
	}
}

func TestErrorsCarryExitCodeAndLastLines(t *testing.T) {
	h := &fakeHost{fn: func([]string) (execx.Result, error) {
		return execx.Result{Stderr: "1\n2\n3\n4\n5\n6\nfalta la imagen X"}, &execx.ExitError{Name: "ssh", Code: 1}
	}}
	err := newD(t, h).Check(context.Background(), "s0")
	var de *Error
	if !errors.As(err, &de) || de.Code != 1 || !strings.Contains(err.Error(), "falta la imagen X") || strings.Contains(err.Error(), "\n1\n") {
		t.Fatalf("err=%v", err)
	}
	var ee *execx.ExitError
	if !errors.As(err, &ee) {
		t.Fatal("ExitError must stay reachable with errors.As")
	}
}

func TestTagVariable(t *testing.T) {
	if v, err := TagVariable("s12"); err != nil || v != "S12_TAG" {
		t.Fatalf("%q %v", v, err)
	}
}
