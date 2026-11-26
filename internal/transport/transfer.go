package transport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/fcode09/forge/internal/artifact"
	"github.com/fcode09/forge/internal/config"
)

// MaxArtifactBytes is the default upper bound for one release (10 GiB).
const MaxArtifactBytes int64 = 10 << 30

// SpaceFactor is how many times the artifact size must be free on the host before transferring.
const SpaceFactor = 2

// Request is one transfer of a release directory to the host staging area.
type Request struct {
	LocalDir  string // <out>/<release-id>
	ReleaseID string
	RemoteDir string // deployment.remote_dir
	MaxBytes  int64  // 0 means MaxArtifactBytes
}

// Result describes the staging directory on the host.
type Result struct {
	StagingDir string `json:"staging_dir"`
	Files      int    `json:"files"`
	Bytes      int64  `json:"bytes"`
}

// StagingDir returns <remote_dir>/incoming/<release-id> and validates both parts.
func StagingDir(remoteDir, releaseID string) (string, error) {
	if !config.ValidReleaseLabel(releaseID) {
		return "", fmt.Errorf("invalid release id %q", releaseID)
	}
	if !path.IsAbs(remoteDir) || path.Clean(remoteDir) != remoteDir || !safeArg.MatchString(remoteDir) {
		return "", fmt.Errorf("invalid remote directory %q", remoteDir)
	}
	return path.Join(remoteDir, "incoming", releaseID), nil
}

// Transfer verifies the artifact locally, checks the host has room, uploads every file (checksums last, so its
// presence means the upload is complete) and has the host verify the checksums with sha256sum before returning.
// A failure after the staging directory was created removes it again; a directory that already existed is never touched.
func (c *Client) Transfer(ctx context.Context, r Request) (*Result, error) {
	staging, err := StagingDir(r.RemoteDir, r.ReleaseID)
	if err != nil {
		return nil, err
	}
	if err := artifact.Verify(r.LocalDir); err != nil {
		return nil, fmt.Errorf("local artifact is not valid: %w", err)
	}
	files, total, err := listFiles(r.LocalDir)
	if err != nil {
		return nil, err
	}
	limit := r.MaxBytes
	if limit == 0 {
		limit = MaxArtifactBytes
	}
	if total > limit {
		return nil, fmt.Errorf("artifact is %d bytes, over the limit of %d", total, limit)
	}

	incoming := path.Dir(staging)
	if res, err := c.Run(ctx, "mkdir", "-p", incoming); err != nil {
		return nil, fmt.Errorf("create %s: %w%s", incoming, err, stderrSuffix(res))
	}
	if err := c.checkSpace(ctx, r.RemoteDir, total); err != nil {
		return nil, err
	}

	// Without -p: fails if this release was already staged, and then nothing is cleaned up.
	if res, err := c.Run(ctx, "mkdir", "-m", "0700", staging); err != nil {
		return nil, fmt.Errorf("create staging %s (already uploaded?): %w%s", staging, err, stderrSuffix(res))
	}
	cleanup := func() {
		if res, err := c.Run(context.WithoutCancel(ctx), "rm", "-rf", "--", staging); err != nil {
			c.Log.Warn("could not remove partial staging", "dir", staging, "err", err, "stderr", strings.TrimSpace(res.Stderr))
		}
	}
	fail := func(err error) (*Result, error) { cleanup(); return nil, err }

	if res, err := c.Run(ctx, "mkdir", "-m", "0700", path.Join(staging, artifact.ImagesDir)); err != nil {
		return fail(fmt.Errorf("create %s/images: %w%s", staging, err, stderrSuffix(res)))
	}
	for _, rel := range files {
		c.Log.Info("uploading", "file", rel)
		if err := c.UploadFile(ctx, filepath.Join(r.LocalDir, filepath.FromSlash(rel)), path.Join(staging, rel)); err != nil {
			return fail(err)
		}
	}
	if res, err := c.Run(ctx, "env", "-C", staging, "sha256sum", "--check", "--strict", "--quiet", artifact.ChecksumsFile); err != nil {
		return fail(fmt.Errorf("remote checksum verification failed: %w%s", err, stderrSuffix(res)))
	}
	c.Log.Info("transfer verified on host", "dir", staging)
	return &Result{StagingDir: staging, Files: len(files), Bytes: total}, nil
}

func (c *Client) checkSpace(ctx context.Context, remoteDir string, total int64) error {
	res, err := c.Run(ctx, "df", "-Pk", remoteDir)
	if err != nil {
		return fmt.Errorf("check free space on %s: %w%s", remoteDir, err, stderrSuffix(res))
	}
	avail, err := parseDfAvailable(res.Stdout)
	if err != nil {
		return err
	}
	if need := total * SpaceFactor; avail < need {
		return fmt.Errorf("not enough space on the host: %d bytes free in %s, need %d", avail, remoteDir, need)
	}
	return nil
}

// parseDfAvailable reads the Available column (1K blocks) of `df -Pk` and returns bytes.
func parseDfAvailable(out string) (int64, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return 0, errors.New("unexpected df output")
	}
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 4 {
		return 0, errors.New("unexpected df output")
	}
	kb, err := strconv.ParseInt(f[3], 10, 64)
	if err != nil || kb < 0 {
		return 0, fmt.Errorf("unexpected df output: %q", f[3])
	}
	return kb * 1024, nil
}

// listFiles returns the relative paths to upload (images first, then manifest and metadata, checksums last)
// and their total size. Verify has already rejected anything that is not a plain, listed file.
func listFiles(dir string) ([]string, int64, error) {
	var rels []string
	var total int64
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if err := artifact.SafeRelPath(rel); err != nil {
			return err
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		total += st.Size()
		rels = append(rels, rel)
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	rank := func(s string) int {
		switch {
		case s == artifact.ChecksumsFile:
			return 3
		case strings.HasPrefix(s, artifact.ImagesDir+"/"):
			return 0
		}
		return 1
	}
	sort.SliceStable(rels, func(i, j int) bool {
		if ri, rj := rank(rels[i]), rank(rels[j]); ri != rj {
			return ri < rj
		}
		return rels[i] < rels[j]
	})
	return rels, total, nil
}
