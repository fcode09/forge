package transport

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/fcode09/forge/internal/config"
)

// PruneStaging removes the oldest staged releases under <remote_dir>/incoming so that at most keep remain.
// The release named in protect is never removed and counts towards keep. Only entries whose name is a valid
// release ID are considered; anything else in the directory is left alone. This only touches staging
// directories: it never removes loaded Docker images, which `deltactl rollback` needs.
func (c *Client) PruneStaging(ctx context.Context, remoteDir string, keep int, protect string) ([]string, error) {
	if keep < 1 {
		return nil, fmt.Errorf("keep must be at least 1, got %d", keep)
	}
	probe, err := StagingDir(remoteDir, "probe")
	if err != nil {
		return nil, err
	}
	incoming := path.Dir(probe)
	res, err := c.Run(ctx, "ls", "-1t", incoming)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w%s", incoming, err, stderrSuffix(res))
	}
	var names []string // newest first
	for _, n := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if config.ValidReleaseLabel(n) {
			names = append(names, n)
		}
	}
	kept := 0
	for _, n := range names {
		if n == protect {
			kept++
		}
	}
	var removed []string
	for _, n := range names {
		if n == protect {
			continue
		}
		if kept < keep {
			kept++
			continue
		}
		if res, err := c.Run(ctx, "rm", "-rf", "--", path.Join(incoming, n)); err != nil {
			return removed, fmt.Errorf("remove %s: %w%s", n, err, stderrSuffix(res))
		}
		removed = append(removed, n)
	}
	return removed, nil
}
