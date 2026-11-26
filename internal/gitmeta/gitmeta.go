// Package gitmeta reads the commit and working-tree state of a project with the git CLI.
package gitmeta

import (
	"context"
	"strings"

	"github.com/fcode09/forge/internal/execx"
)

// Info describes the Git state of a project directory.
type Info struct {
	IsRepo bool   // false when dir is not in a Git work tree or has no commits
	Commit string // short commit SHA, empty when !IsRepo
	Dirty  bool   // uncommitted or untracked changes exist
}

// Read inspects dir. A directory that is not a repository is not an error: IsRepo is false.
func Read(ctx context.Context, r execx.Runner, dir string) (Info, error) {
	res, err := r.Run(ctx, "git", "-C", dir, "rev-parse", "--short=7", "HEAD")
	if err != nil {
		return Info{}, nil
	}
	commit := strings.TrimSpace(res.Stdout)
	if commit == "" {
		return Info{}, nil
	}
	st, err := r.Run(ctx, "git", "-C", dir, "status", "--porcelain")
	if err != nil {
		return Info{}, err
	}
	return Info{IsRepo: true, Commit: commit, Dirty: strings.TrimSpace(st.Stdout) != ""}, nil
}
