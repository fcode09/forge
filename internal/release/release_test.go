package release

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fcode09/forge/internal/config"
	"github.com/fcode09/forge/internal/gitmeta"
)

func TestNewID(t *testing.T) {
	now := time.Date(2026, 11, 25, 22, 51, 3, 0, time.FixedZone("x", -5*3600))
	cases := []struct {
		g    gitmeta.Info
		want string
	}{
		{gitmeta.Info{IsRepo: true, Commit: "3cb19d0"}, "20261126T035103Z-3cb19d0"},
		{gitmeta.Info{IsRepo: true, Commit: "3cb19d0", Dirty: true}, "20261126T035103Z-3cb19d0-dirty"},
		{gitmeta.Info{}, "20261126T035103Z-nogit"},
	}
	for _, c := range cases {
		got := NewID(now, c.g)
		if got != c.want || !config.ValidReleaseLabel(got) {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
}

func TestValidate(t *testing.T) {
	for _, bad := range []string{"latest", "", "a b", "../x", "x:y"} {
		if Validate(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := Validate("v1.2.3-rc1"); err != nil {
		t.Fatal(err)
	}
}

func TestLastRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadLast(dir); err == nil {
		t.Fatal("expected error before any build")
	}
	want := Last{ReleaseID: "20261126T035103Z-3cb19d0", Commit: "3cb19d0", CreatedUTC: "2026-11-26T03:51:03Z"}
	if err := SaveLast(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadLast(dir)
	if err != nil || got != want {
		t.Fatalf("got %+v err %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateDir, lastFile), []byte(`{"release_id":"../x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLast(dir); err == nil {
		t.Fatal("unsafe release id accepted")
	}
}

func TestHistoryAppendAndRead(t *testing.T) {
	dir := t.TempDir()
	if evs, err := ReadEvents(dir); err != nil || len(evs) != 0 {
		t.Fatalf("empty history: %v %v", evs, err)
	}
	for _, e := range []Event{
		{TimeUTC: "t1", Action: "deploy", ReleaseID: "r1", Instance: "colorado", Services: []string{"s0"}, Outcome: "deployed"},
		{TimeUTC: "t2", Action: "rollback", Instance: "colorado", Outcome: "rolled_back", Error: "x"},
	} {
		if err := AppendEvent(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	evs, err := ReadEvents(dir)
	if err != nil || len(evs) != 2 || evs[0].ReleaseID != "r1" || evs[1].Action != "rollback" || evs[1].Error != "x" {
		t.Fatalf("evs=%+v err=%v", evs, err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateDir, historyFile), []byte("{\"action\":\"deploy\"}\nnot json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEvents(dir); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err=%v", err)
	}
}

func TestPruneLocal(t *testing.T) {
	dist := t.TempDir()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, name := range []string{"r1", "r2", "r3", "r4", "r5"} {
		p := filepath.Join(dist, name)
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, base.Add(time.Duration(i)*time.Hour), base.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	// Things that are not releases are never touched.
	if err := os.WriteFile(filepath.Join(dist, "notes.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dist, ".partial-x"), 0o755); err != nil {
		t.Fatal(err)
	}

	removed, err := PruneLocal(dist, 2, "r1") // r1 is protected and counts: keep r1 + r5
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(removed)
	if strings.Join(removed, ",") != "r2,r3,r4" {
		t.Fatalf("removed=%v", removed)
	}
	for _, keep := range []string{"r1", "r5", "notes.txt", ".partial-x"} {
		if _, err := os.Stat(filepath.Join(dist, keep)); err != nil {
			t.Errorf("%s was removed", keep)
		}
	}
	if _, err := PruneLocal(dist, 0); err == nil {
		t.Fatal("keep 0 accepted")
	}
	if r, err := PruneLocal(filepath.Join(dist, "missing"), 2); err != nil || r != nil {
		t.Fatalf("missing dir: %v %v", r, err)
	}
}
