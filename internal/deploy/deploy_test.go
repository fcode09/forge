package deploy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

type fakeOps struct {
	calls []string
	tags  map[string]string // current .env.instance tags
	prev  map[string]string // what `rollback` restores
	fail  map[string]error  // call -> error
}

func newOps() *fakeOps {
	return &fakeOps{
		tags: map[string]string{"s0": "1.0.0", "s1": "2.0.0"},
		prev: map[string]string{"s0": "1.0.0", "s1": "2.0.0"},
		fail: map[string]error{},
	}
}

func (f *fakeOps) do(call string) error {
	f.calls = append(f.calls, call)
	return f.fail[call]
}
func (f *fakeOps) Load(_ context.Context, file string) error { return f.do("load " + file) }
func (f *fakeOps) GetTag(_ context.Context, s string) (string, error) {
	if err := f.do("get " + s); err != nil {
		return "", err
	}
	return f.tags[s], nil
}
func (f *fakeOps) SetTag(_ context.Context, s, tag string) error {
	if err := f.do("set " + s + " " + tag); err != nil {
		return err
	}
	f.tags[s] = tag
	return nil
}
func (f *fakeOps) Check(_ context.Context, s string) error  { return f.do("check " + s) }
func (f *fakeOps) Deploy(_ context.Context, s string) error { return f.do("deploy " + s) }
func (f *fakeOps) Rollback(_ context.Context, s string) error {
	if err := f.do("rollback " + s); err != nil {
		return err
	}
	f.tags[s] = f.prev[s]
	return nil
}
func (f *fakeOps) Status(context.Context) (string, error) { return "all healthy", f.do("status") }

var log = slog.New(slog.NewTextHandler(io.Discard, nil))

func plan() Plan {
	return Plan{ReleaseID: "r2", StagingDir: "/srv/forge/incoming/r2", Images: []Image{
		{"s0", "images/auth.tar.gz"}, {"s1", "images/gue.tar.gz"}}}
}

func joined(f *fakeOps) string { return strings.Join(f.calls, "\n") }

func TestSuccess(t *testing.T) {
	f := newOps()
	res, err := Apply(context.Background(), f, log, plan())
	if err != nil || res.Outcome != Deployed || res.Status != "all healthy" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	want := `get s0
get s1
load /srv/forge/incoming/r2/images/auth.tar.gz
load /srv/forge/incoming/r2/images/gue.tar.gz
set s0 r2
set s1 r2
check s0
check s1
deploy s0
deploy s1
status`
	if joined(f) != want {
		t.Fatalf("calls:\n%s\nwant:\n%s", joined(f), want)
	}
}

func TestLoadFailureChangesNoTag(t *testing.T) {
	f := newOps()
	f.fail["load /srv/forge/incoming/r2/images/gue.tar.gz"] = errors.New("disk")
	res, err := Apply(context.Background(), f, log, plan())
	if err == nil || res.Outcome != Aborted || res.Failed != "load s1" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if f.tags["s0"] != "1.0.0" || f.tags["s1"] != "2.0.0" || strings.Contains(joined(f), "set ") {
		t.Fatalf("tags changed: %v\n%s", f.tags, joined(f))
	}
}

func TestSetTagFailureRestoresTags(t *testing.T) {
	f := newOps()
	f.fail["set s1 r2"] = errors.New("invalid")
	res, err := Apply(context.Background(), f, log, plan())
	if err == nil || res.Outcome != Aborted || res.Failed != "set tag of s1" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if f.tags["s0"] != "1.0.0" || f.tags["s1"] != "2.0.0" {
		t.Fatalf("tags not restored: %v", f.tags)
	}
}

func TestCheckFailureRestoresTagsAndNeverRollsBack(t *testing.T) {
	f := newOps()
	f.fail["check s1"] = errors.New("falta la imagen")
	res, err := Apply(context.Background(), f, log, plan())
	if err == nil || res.Outcome != Aborted || res.Failed != "check s1" || len(res.Problems) != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if f.tags["s0"] != "1.0.0" || f.tags["s1"] != "2.0.0" {
		t.Fatalf("tags not restored: %v", f.tags)
	}
	if strings.Contains(joined(f), "rollback") || strings.Contains(joined(f), "deploy ") {
		t.Fatalf("nothing was deployed, so nothing may be rolled back:\n%s", joined(f))
	}
}

func TestDeployFailureOnSecondServiceRollsBackBothInReverse(t *testing.T) {
	f := newOps()
	f.fail["deploy s1"] = errors.New("unhealthy")
	res, err := Apply(context.Background(), f, log, plan())
	if err == nil || res.Outcome != RolledBack || res.Failed != "deploy s1" || len(res.Problems) != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	calls := joined(f)
	if !strings.HasSuffix(calls, "deploy s0\ndeploy s1\nrollback s1\nget s1\nrollback s0\nget s0") {
		t.Fatalf("calls:\n%s", calls)
	}
	if f.tags["s0"] != "1.0.0" || f.tags["s1"] != "2.0.0" {
		t.Fatalf("tags: %v", f.tags)
	}
}

func TestDeployFailureOnFirstServiceRestoresTheUntouchedOne(t *testing.T) {
	f := newOps()
	f.fail["deploy s0"] = errors.New("unhealthy")
	res, err := Apply(context.Background(), f, log, plan())
	if err == nil || res.Outcome != RolledBack {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if strings.Contains(joined(f), "deploy s1") || strings.Contains(joined(f), "rollback s1") {
		t.Fatalf("s1 was never deployed:\n%s", joined(f))
	}
	if f.tags["s1"] != "2.0.0" {
		t.Fatalf("untouched service tag not restored: %v", f.tags)
	}
}

func TestRollbackFailureIsReported(t *testing.T) {
	f := newOps()
	f.fail["deploy s0"] = errors.New("unhealthy")
	f.fail["rollback s0"] = errors.New("interrupted run blocks new operations")
	res, err := Apply(context.Background(), f, log, plan())
	if err == nil || res.Outcome != RollbackFailed || len(res.Problems) == 0 || !strings.Contains(res.Problems[0], "rollback s0") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestRollbackToTheWrongVersionIsDetected(t *testing.T) {
	f := newOps()
	f.fail["deploy s0"] = errors.New("unhealthy")
	f.prev["s0"] = "0.9.0" // deltactl's .previous is older than what was running
	res, err := Apply(context.Background(), f, log, plan())
	if err == nil || res.Outcome != RollbackFailed || !strings.Contains(strings.Join(res.Problems, ";"), `"0.9.0" but "1.0.0"`) {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestRecoveryRunsEvenIfTheContextWasCancelled(t *testing.T) {
	f := newOps()
	ctx, cancel := context.WithCancel(context.Background())
	f.fail["deploy s0"] = errors.New("cancelled")
	g := &cancelOps{fakeOps: f, cancel: cancel}
	res, _ := Apply(ctx, g, log, plan())
	if res.Outcome != RolledBack || !g.rollbackSawLiveContext {
		t.Fatalf("res=%+v liveCtx=%v", res, g.rollbackSawLiveContext)
	}
}

type cancelOps struct {
	*fakeOps
	cancel                 func()
	rollbackSawLiveContext bool
}

func (c *cancelOps) Deploy(ctx context.Context, s string) error {
	c.cancel()
	return c.fakeOps.Deploy(ctx, s)
}
func (c *cancelOps) Rollback(ctx context.Context, s string) error {
	c.rollbackSawLiveContext = ctx.Err() == nil
	return c.fakeOps.Rollback(ctx, s)
}

func TestEmptyPlanAndTagReadFailure(t *testing.T) {
	if res, err := Apply(context.Background(), newOps(), log, Plan{ReleaseID: "r"}); err == nil || res.Outcome != Aborted {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	f := newOps()
	f.fail["get s0"] = errors.New("no such instance")
	res, err := Apply(context.Background(), f, log, plan())
	if err == nil || res.Outcome != Aborted || strings.Contains(joined(f), "load") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestDescribe(t *testing.T) {
	got := Describe(plan(), "/srv/itc-deploy/deltactl", "colorado")
	if got[0] != "/srv/itc-deploy/deltactl env colorado get S0_TAG" || got[len(got)-1] != "/srv/itc-deploy/deltactl status colorado" ||
		!strings.Contains(strings.Join(got, "\n"), "deploy colorado s1") || len(got) != 11 {
		t.Fatalf("%q", got)
	}
}

func TestNoCurrentTagMeansFirstDeploymentIsManual(t *testing.T) {
	f := newOps()
	f.tags["s1"] = ""
	res, err := Apply(context.Background(), f, log, plan())
	if err == nil || res.Outcome != Aborted || !strings.Contains(err.Error(), "first deployment") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if strings.Contains(joined(f), "load") || strings.Contains(joined(f), "set ") {
		t.Fatalf("nothing may change:\n%s", joined(f))
	}
}
