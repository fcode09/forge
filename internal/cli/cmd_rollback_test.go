package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fcode09/forge/internal/release"
)

func TestRollbackNeedsYes(t *testing.T) {
	f := &fakeExec{getTag: "r2"}
	a, out, errb := transferApp(t, f)
	if code := run(a, "rollback"); code != ExitUsage {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "s0   r2") || !strings.Contains(out.String(), "--yes") {
		t.Fatalf("out=%q", out.String())
	}
	for _, r := range f.remotes {
		if strings.Contains(r, "rollback") {
			t.Fatalf("rolled back without --yes: %q", f.remotes)
		}
	}
}

func TestRollbackYes(t *testing.T) {
	f := &fakeExec{getTag: "r2", afterRollbackTag: "1.0.0"}
	a, out, errb := transferApp(t, f)
	if code := run(a, "rollback", "--yes"); code != ExitOK {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	if !hasRemote(f, dc+" rollback colorado s0 --yes") || !strings.Contains(out.String(), "rolled back") {
		t.Fatalf("remotes=%q out=%q", f.remotes, out.String())
	}
	evs, err := release.ReadEvents(mustWd(t, a))
	if err != nil || len(evs) != 1 || evs[0].Action != "rollback" || evs[0].Outcome != "rolled_back" || evs[0].Instance != "colorado" {
		t.Fatalf("events=%+v err=%v", evs, err)
	}
}

func TestRollbackFailureExitCode(t *testing.T) {
	f := &fakeExec{getTag: "r2", failIf: []string{dc + " rollback colorado s0"}}
	a, _, errb := transferApp(t, f)
	if code := run(a, "rollback", "--yes"); code != ExitRollbackFailed || !strings.Contains(errb.String(), "failed for instance colorado") {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	if code := run(a, "rollback", "--yes", "--service", "s9"); code != ExitUsage {
		t.Fatalf("bad service: %d", code)
	}
}

func TestDeployRecordsHistoryAndReleasesListsIt(t *testing.T) {
	f := &fakeExec{getTag: "1.0.0"}
	a, out, errb := transferApp(t, f)
	if code := run(a, "deploy"); code != ExitOK {
		t.Fatalf("deploy: %d %s", code, errb.String())
	}
	out.Reset()
	if code := run(a, "releases"); code != ExitOK {
		t.Fatal(code)
	}
	if !strings.Contains(out.String(), "deploy") || !strings.Contains(out.String(), "20261125T220000Z-abc1234") || !strings.Contains(out.String(), "deployed") {
		t.Fatalf("out=%q", out.String())
	}
	out.Reset()
	if code := run(a, "releases", "--json"); code != ExitOK {
		t.Fatal(code)
	}
	var evs []release.Event
	if err := json.Unmarshal([]byte(out.String()), &evs); err != nil || len(evs) != 1 || evs[0].Commit != "abc1234" || evs[0].Services[0] != "s0" {
		t.Fatalf("out=%q err=%v", out.String(), err)
	}

	// A failed deploy is recorded too, newest first.
	f.failIf = []string{dc + " deploy colorado s0"}
	run(a, "deploy", "--tag", "r-second")
	out.Reset()
	run(a, "releases", "-n", "1")
	if !strings.Contains(out.String(), "r-second") || !strings.Contains(out.String(), "rolled_back") || strings.Contains(out.String(), "20261125T220000Z-abc1234") {
		t.Fatalf("out=%q", out.String())
	}
}

func TestReleasesEmptyAndStatus(t *testing.T) {
	f := &fakeExec{statusOut: "instance colorado: 1/1 healthy\n"}
	a, out, errb := transferApp(t, f)
	if code := run(a, "releases"); code != ExitOK || !strings.Contains(out.String(), "no deploys or rollbacks") {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
	out.Reset()
	if code := run(a, "status"); code != ExitOK || !strings.Contains(out.String(), "1/1 healthy") {
		t.Fatalf("code=%d out=%q err=%s", code, out.String(), errb.String())
	}
	if !hasRemote(f, dc+" status colorado") {
		t.Fatalf("remotes=%q", f.remotes)
	}
	out.Reset()
	if err := release.AppendEvent(mustWd(t, a), release.Event{TimeUTC: "2026-11-25T22:00:00Z", Action: "deploy", ReleaseID: "r9", Instance: "colorado", Outcome: "deployed"}); err != nil {
		t.Fatal(err)
	}
	run(a, "status")
	if !strings.Contains(out.String(), "last local event: 2026-11-25T22:00:00Z deploy r9 (deployed)") {
		t.Fatalf("out=%q", out.String())
	}
}

func TestRetentionAfterSuccessfulDeploy(t *testing.T) {
	f := &fakeExec{getTag: "1.0.0", stagingList: "r-new\nold-b\nold-a\n"}
	a, _, errb := transferApp(t, f)
	wd := mustWd(t, a)
	// keep 1: only the release being deployed stays.
	cfgPath := filepath.Join(wd, "forge.yaml")
	b, _ := os.ReadFile(cfgPath)
	if err := os.WriteFile(cfgPath, []byte(strings.Replace(string(b), "releases: 5", "releases: 1", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, old := range []string{"old-a", "old-b"} {
		if err := os.MkdirAll(filepath.Join(wd, "dist", old), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if code := run(a, "deploy"); code != ExitOK {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	for _, old := range []string{"old-a", "old-b"} {
		if _, err := os.Stat(filepath.Join(wd, "dist", old)); err == nil {
			t.Errorf("local %s survived retention", old)
		}
	}
	if _, err := os.Stat(filepath.Join(wd, "dist", "20261125T220000Z-abc1234")); err != nil {
		t.Fatal("the current release must be kept")
	}
	if !hasRemote(f, "rm -rf -- /srv/forge/incoming/old-a") || !hasRemote(f, "rm -rf -- /srv/forge/incoming/old-b") {
		t.Fatalf("remotes=%q", f.remotes)
	}
	// Loaded Docker images are never removed.
	for _, r := range f.remotes {
		if strings.Contains(r, "docker") || strings.Contains(r, "prune") {
			t.Fatalf("must not touch Docker images: %q", r)
		}
	}
}

func TestFailedDeployDoesNotPrune(t *testing.T) {
	f := &fakeExec{getTag: "1.0.0", stagingList: "x\n", failIf: []string{dc + " deploy colorado s0"}}
	a, _, _ := transferApp(t, f)
	run(a, "deploy")
	for _, r := range f.remotes {
		if strings.HasPrefix(r, "ls ") || strings.HasPrefix(r, "rm ") && !strings.Contains(r, "incoming/20261125") {
			t.Fatalf("pruned after a failed deploy: %q", f.remotes)
		}
	}
}
