package gitmeta

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/fcode09/forge/internal/execx"
)

// TestForgeOwnFilesAreNotChanges runs the real git CLI: FORGE's configuration and output directories must not
// make the work tree dirty, while a real change to the project must.
func TestForgeOwnFilesAreNotChanges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write("app.js", "1")
	git("add", "-A")
	git("commit", "-q", "-m", "init")

	read := func() Info {
		t.Helper()
		g, err := Read(context.Background(), execx.Exec{}, dir)
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	if g := read(); !g.IsRepo || g.Dirty {
		t.Fatalf("clean repo reported as %+v", g)
	}

	// Untracked forge.yaml, dist/ and .forge/: FORGE's own files.
	write("forge.yaml", "version: 1")
	write("dist/20261126T000000Z-abc1234/images/a.tar.gz", "x")
	write(".forge/history.jsonl", "{}")
	if g := read(); g.Dirty {
		t.Fatalf("FORGE's own files must not count as changes: %+v", g)
	}

	// A modified forge.yaml that is tracked does not count either.
	git("add", "forge.yaml")
	git("commit", "-q", "-m", "config")
	write("forge.yaml", "version: 1 # edited")
	if g := read(); g.Dirty {
		t.Fatalf("an edited forge.yaml must not count: %+v", g)
	}

	// A real change to the project does.
	write("app.js", "2")
	if g := read(); !g.Dirty {
		t.Fatalf("a modified project file must count: %+v", g)
	}
}
