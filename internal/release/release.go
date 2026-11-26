// Package release creates the immutable release identifiers used as image tags.
package release

import (
	"fmt"
	"time"

	"github.com/fcode09/forge/internal/config"
	"github.com/fcode09/forge/internal/gitmeta"
)

// NewID returns <UTC timestamp>-<commit>, with "-dirty" appended for a modified work tree and
// "nogit" in place of the commit outside a repository. The timestamp keeps it unique and sortable.
func NewID(now time.Time, g gitmeta.Info) string {
	commit := "nogit"
	if g.IsRepo {
		commit = g.Commit
	}
	id := fmt.Sprintf("%s-%s", now.UTC().Format("20060102T150405Z"), commit)
	if g.Dirty {
		id += "-dirty"
	}
	return id
}

// Validate checks a user supplied release ID (for example from --tag).
func Validate(id string) error {
	if id == "latest" {
		return fmt.Errorf("release id %q is a mutable tag and cannot identify a release", id)
	}
	if !config.ValidReleaseLabel(id) {
		return fmt.Errorf("release id %q is not valid: use letters, digits, '.', '_' and '-' (max 128, no leading symbol)", id)
	}
	return nil
}
