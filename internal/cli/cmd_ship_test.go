package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fcode09/forge/internal/execx"
	"github.com/fcode09/forge/internal/release"
)

// fakeScp records the scp invocation instead of running it.
type fakeScp struct {
	calls [][]string
	dir   string
	err   error
}

func (f *fakeScp) RunInteractive(_ context.Context, dir, name string, args ...string) error {
	f.dir = dir
	f.calls = append(f.calls, append([]string{name}, args...))
	return f.err
}

const shipYAML = `version: 1
project: {name: app}
build:
  images:
    - {name: backend, image: app}
upload:
  target: mpa
  remote_dir: images
retention:
  releases: 2
`

// shipApp is a packaged-capable app whose forge.yaml only has `upload` (no deployment, no tests).
func shipApp(t *testing.T) (*App, *strings.Builder, *strings.Builder, *fakeScp, string) {
	t.Helper()
	a, out, errb, _ := buildApp(t, gitRunner{commit: "abc1234"})
	a.Streamer = fakeStreamer{}
	wd, _ := a.Getwd()
	if err := os.WriteFile(filepath.Join(wd, "forge.yaml"), []byte(shipYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeScp{}
	a.Interactive = f
	return a, out, errb, f, wd
}

func TestShipBuildsPackagesAndUploadsOnce(t *testing.T) {
	a, out, errb, f, wd := shipApp(t)
	if code := run(a, "ship"); code != ExitOK {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	id := "20261125T220000Z-abc1234"
	if len(f.calls) != 1 || f.calls[0][0] != "scp" {
		t.Fatalf("expected one scp, got %v", f.calls)
	}
	if f.dir != filepath.Join(wd, "dist") {
		t.Fatalf("scp must run in dist/, ran in %q", f.dir)
	}
	args := f.calls[0]
	if args[len(args)-2] != id || args[len(args)-1] != "mpa:images/" {
		t.Fatalf("args=%v", args)
	}
	if !strings.Contains(out.String(), "uploaded to mpa:images/"+id) ||
		!strings.Contains(out.String(), "cd images/"+id+" && sha256sum -c checksums.sha256") {
		t.Fatalf("out=%q", out.String())
	}
	evs, err := release.ReadEvents(wd)
	if err != nil || len(evs) != 1 || evs[0].Action != "ship" || evs[0].Target != "mpa" || evs[0].Outcome != "uploaded" || evs[0].ReleaseID != id {
		t.Fatalf("evs=%+v err=%v", evs, err)
	}
	out.Reset()
	if code := run(a, "releases"); code != ExitOK || !strings.Contains(out.String(), "ship") || !strings.Contains(out.String(), "mpa") {
		t.Fatalf("releases: code=%d out=%q", code, out.String())
	}
}

func TestShipDryRunTouchesNothing(t *testing.T) {
	a, out, _, f, wd := shipApp(t)
	if code := run(a, "ship", "--dry-run"); code != ExitOK {
		t.Fatalf("code=%d", code)
	}
	if len(f.calls) != 0 || !strings.Contains(out.String(), "would run: in ") || !strings.Contains(out.String(), "scp -r") {
		t.Fatalf("calls=%v out=%q", f.calls, out.String())
	}
	if _, err := os.Stat(filepath.Join(wd, "dist")); err == nil {
		t.Fatal("dry run must not package anything")
	}
	if evs, _ := release.ReadEvents(wd); len(evs) != 0 {
		t.Fatalf("dry run recorded %v", evs)
	}
}

func TestShipReusesPackagedRelease(t *testing.T) {
	a, _, errb, f, _ := shipApp(t)
	if code := run(a, "build"); code != ExitOK {
		t.Fatal(code, errb.String())
	}
	if code := run(a, "package"); code != ExitOK {
		t.Fatal(code, errb.String())
	}
	if code := run(a, "ship", "--release", "20261125T220000Z-abc1234"); code != ExitOK {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	if len(f.calls) != 1 {
		t.Fatalf("calls=%v", f.calls)
	}
	errb.Reset()
	if code := run(a, "ship", "--release", "nope-1"); code != ExitError || !strings.Contains(errb.String(), "not usable") {
		t.Fatalf("missing release: code=%d err=%q", code, errb.String())
	}
}

func TestShipFailureIsRecordedAndReported(t *testing.T) {
	a, _, errb, f, wd := shipApp(t)
	f.err = &execx.ExitError{Name: "scp", Code: 1}
	if code := run(a, "ship"); code != ExitError || !strings.Contains(errb.String(), "scp failed") {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
	evs, _ := release.ReadEvents(wd)
	if len(evs) != 1 || evs[0].Outcome != "failed" || evs[0].Error == "" {
		t.Fatalf("evs=%+v", evs)
	}
}

func TestShipNeedsUploadSection(t *testing.T) {
	a, _, errb, _ := buildApp(t, gitRunner{commit: "abc1234"}) // the template has deployment AND upload
	wd, _ := a.Getwd()
	noUpload := strings.Replace(string(mustRead(t, filepath.Join(wd, "forge.yaml"))), "target: mpa", "target: \"\"", 1)
	noUpload = strings.Replace(noUpload, "remote_dir: images", "", 1)
	if err := os.WriteFile(filepath.Join(wd, "forge.yaml"), []byte(noUpload), 0o644); err != nil {
		t.Fatal(err)
	}
	a.Interactive = &fakeScp{}
	if code := run(a, "ship"); code != ExitError || !strings.Contains(errb.String(), "no upload section") {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
}

func TestShipJSONAndRetention(t *testing.T) {
	a, out, errb, _, wd := shipApp(t)
	for _, old := range []string{"20260101T000000Z-aaaaaaa", "20260102T000000Z-bbbbbbb"} {
		if err := os.MkdirAll(filepath.Join(wd, "dist", old), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if code := run(a, "ship", "--json"); code != ExitOK {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	var res struct {
		ReleaseID string `json:"release_id"`
		RemoteDir string `json:"remote_dir"`
		Verify    string `json:"verify_command"`
		Files     int    `json:"files"`
	}
	if err := json.Unmarshal([]byte(out.String()), &res); err != nil || res.Files != 4 || !strings.HasSuffix(res.RemoteDir, res.ReleaseID) || !strings.Contains(res.Verify, "sha256sum") {
		t.Fatalf("out=%q err=%v", out.String(), err)
	}
	entries, _ := os.ReadDir(filepath.Join(wd, "dist"))
	if len(entries) != 2 { // retention.releases: 2 -> the new one plus one older
		t.Fatalf("dist has %d entries", len(entries))
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
