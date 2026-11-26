package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const dc = "/srv/itc-deploy/deltactl"

func hasRemote(f *fakeExec, want string) bool {
	for _, r := range f.remotes {
		if r == want {
			return true
		}
	}
	return false
}

func indexOf(f *fakeExec, prefix string) int {
	for i, r := range f.remotes {
		if strings.HasPrefix(r, prefix) {
			return i
		}
	}
	return -1
}

func TestDeploySuccess(t *testing.T) {
	f := &fakeExec{}
	a, out, errb := transferApp(t, f)
	if code := run(a, "deploy"); code != ExitOK {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	id := "20261125T220000Z-abc1234"
	if !strings.Contains(out.String(), "release "+id+" deployed to instance colorado") {
		t.Fatalf("out=%q", out.String())
	}
	// Order: upload and verify first, then deltactl get, load, set, check, deploy, status.
	order := []string{
		"env -C /srv/forge/incoming/" + id + " sha256sum",
		dc + " env colorado get S0_TAG",
		dc + " load /srv/forge/incoming/" + id + "/images/backend.tar.gz",
		dc + " env colorado set S0_TAG " + id,
		dc + " check colorado s0",
		dc + " deploy colorado s0",
		dc + " status colorado",
	}
	prev := -1
	for _, p := range order {
		i := indexOf(f, p)
		if i <= prev {
			t.Fatalf("%q out of order (index %d after %d): %q", p, i, prev, f.remotes)
		}
		prev = i
	}
	for _, r := range f.remotes {
		if strings.Contains(r, "rollback") {
			t.Fatalf("unexpected rollback: %q", f.remotes)
		}
	}
}

func TestDeployFailureRollsBackWithExitCode3(t *testing.T) {
	f := &fakeExec{failIf: []string{dc + " deploy colorado s0"}}
	a, _, errb := transferApp(t, f)
	if code := run(a, "deploy"); code != ExitRolledBack {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	if !hasRemote(f, dc+" rollback colorado s0 --yes") || !strings.Contains(errb.String(), "back on the previous version") {
		t.Fatalf("remotes=%q err=%q", f.remotes, errb.String())
	}
}

func TestDeployRollbackFailureExitCode4(t *testing.T) {
	f := &fakeExec{failIf: []string{dc + " deploy colorado s0", dc + " rollback colorado s0"}}
	a, _, errb := transferApp(t, f)
	if code := run(a, "deploy"); code != ExitRollbackFailed {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "could NOT be confirmed") || !strings.Contains(errb.String(), "runs list --instance colorado") {
		t.Fatalf("err=%q", errb.String())
	}
}

func TestDeployCheckFailureRestoresTagWithoutRollback(t *testing.T) {
	f := &fakeExec{failIf: []string{dc + " check colorado s0"}}
	a, _, errb := transferApp(t, f)
	if code := run(a, "deploy"); code != ExitError || !strings.Contains(errb.String(), "aborted at check s0") {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	if !hasRemote(f, dc+" env colorado set S0_TAG 1.0.0") {
		t.Fatalf("old tag not restored: %q", f.remotes)
	}
	for _, r := range f.remotes {
		if strings.Contains(r, "rollback") || strings.Contains(r, " deploy ") {
			t.Fatalf("must not deploy or roll back: %q", f.remotes)
		}
	}
}

func TestDeployDryRunTouchesNothing(t *testing.T) {
	f := &fakeExec{}
	a, out, errb := transferApp(t, f)
	if code := run(a, "deploy", "--dry-run"); code != ExitOK {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	if len(f.remotes) != 0 {
		t.Fatalf("dry run connected: %q", f.remotes)
	}
	for _, want := range []string{"would run: docker build", "would upload to forge-deploy@10.20.0.2:/srv/forge/incoming/", "would run on host: " + dc + " deploy colorado s0"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %s", want, out.String())
		}
	}
	if _, err := os.Stat(filepath.Join(mustWd(t, a), "dist")); err == nil {
		t.Fatal("dry run created dist/")
	}
}

func mustWd(t *testing.T, a *App) string {
	t.Helper()
	wd, err := a.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

func TestDeployExistingReleaseSkippingTransfer(t *testing.T) {
	f := &fakeExec{}
	a, out, errb := transferApp(t, f)
	if code := run(a, "build"); code != ExitOK {
		t.Fatal(errb.String())
	}
	if code := run(a, "package"); code != ExitOK {
		t.Fatal(errb.String())
	}
	f.remotes = nil
	id := "20261125T220000Z-abc1234"
	if code := run(a, "deploy", "--release", id, "--skip-transfer"); code != ExitOK {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	for _, r := range f.remotes {
		if strings.HasPrefix(r, "dd ") || strings.HasPrefix(r, "mkdir") {
			t.Fatalf("--skip-transfer uploaded: %q", f.remotes)
		}
	}
	if !hasRemote(f, dc+" load /srv/forge/incoming/"+id+"/images/backend.tar.gz") || !strings.Contains(out.String(), "deployed") {
		t.Fatalf("remotes=%q out=%q", f.remotes, out.String())
	}
}

func TestDeployFlagValidation(t *testing.T) {
	a, _, errb := transferApp(t, &fakeExec{})
	if code := run(a, "deploy", "--service", "s9"); code != ExitUsage || !strings.Contains(errb.String(), "matches no image") {
		t.Fatalf("service: %d %q", code, errb.String())
	}
	if code := run(a, "deploy", "--release", "latest"); code != ExitUsage {
		t.Fatalf("latest: %d", code)
	}
	if code := run(a, "deploy", "--release", "v1", "--tag", "v2"); code != ExitUsage {
		t.Fatalf("combined: %d", code)
	}
	errb.Reset()
	if code := run(a, "deploy", "--release", "nonexistent"); code != ExitError || !strings.Contains(errb.String(), "not usable") {
		t.Fatalf("missing release: %d %q", code, errb.String())
	}
}

func TestDeployRefusesReleaseForAnotherInstance(t *testing.T) {
	f := &fakeExec{}
	a, _, errb := transferApp(t, f)
	run(a, "build")
	run(a, "package")
	id := "20261125T220000Z-abc1234"
	wd := mustWd(t, a)
	cfgPath := filepath.Join(wd, "forge.yaml")
	b, _ := os.ReadFile(cfgPath)
	if err := os.WriteFile(cfgPath, []byte(strings.Replace(string(b), "instance: colorado", "instance: lima", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	f.remotes = nil
	if code := run(a, "deploy", "--release", id); code != ExitError || !strings.Contains(errb.String(), `packaged for instance "colorado" but forge.yaml targets "lima"`) {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
	if len(f.remotes) != 0 {
		t.Fatalf("must not touch the host: %q", f.remotes)
	}
}

func TestDeployJSON(t *testing.T) {
	f := &fakeExec{}
	a, out, errb := transferApp(t, f)
	if code := run(a, "deploy", "--json"); code != ExitOK {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	// stdout is exactly one JSON document.
	var res struct {
		Outcome   string `json:"outcome"`
		ReleaseID string `json:"release_id"`
	}
	if err := json.Unmarshal([]byte(out.String()), &res); err != nil || res.Outcome != "deployed" || res.ReleaseID == "" {
		t.Fatalf("out=%q err=%v", out.String(), err)
	}
}
