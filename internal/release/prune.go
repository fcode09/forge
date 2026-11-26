package release

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/fcode09/forge/internal/config"
)

// PruneLocal removes the oldest release directories in distDir so that at most keep remain. Directories named
// in protect are never removed (they count towards keep). Only directories whose name is a valid release ID
// are considered, and "oldest" is decided by modification time. It returns the names it removed.
func PruneLocal(distDir string, keep int, protect ...string) ([]string, error) {
	if keep < 1 {
		return nil, fmt.Errorf("keep must be at least 1, got %d", keep)
	}
	entries, err := os.ReadDir(distDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	type rel struct {
		name string
		mod  int64
	}
	var all []rel
	for _, e := range entries {
		if !e.IsDir() || !config.ValidReleaseLabel(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		all = append(all, rel{e.Name(), info.ModTime().UnixNano()})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].mod != all[j].mod {
			return all[i].mod > all[j].mod
		}
		return all[i].name > all[j].name
	})
	keepSet := map[string]bool{}
	for _, p := range protect {
		keepSet[p] = true
	}
	kept := 0
	for _, r := range all {
		if keepSet[r.name] {
			kept++
		}
	}
	var removed []string
	for _, r := range all {
		if keepSet[r.name] {
			continue
		}
		if kept < keep {
			kept++
			continue
		}
		if err := os.RemoveAll(filepath.Join(distDir, r.name)); err != nil {
			return removed, err
		}
		removed = append(removed, r.name)
	}
	return removed, nil
}
