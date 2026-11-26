// Package upload copies a packaged release to a directory in the home of an ssh login with one `scp -r`.
//
// Nothing runs on the server: no mkdir, no checksum, no cleanup. ssh and scp read the user's own
// ~/.ssh/config, so an alias such as "mpa" works as it does in a terminal, including a password prompt. One
// scp is one connection, so a password is asked once.
package upload

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"path/filepath"

	"github.com/fcode09/forge/internal/artifact"
	"github.com/fcode09/forge/internal/config"
	"github.com/fcode09/forge/internal/execx"
)

// Request is one upload.
type Request struct {
	Target    string // upload.target: ssh alias, host or user@host
	RemoteDir string // upload.remote_dir: relative to the remote home, or absolute
	ReleaseID string
	LocalDir  string // <dist>/<release-id>
}

// Result describes what was copied.
type Result struct {
	ReleaseID string `json:"release_id"`
	Target    string `json:"target"`
	RemoteDir string `json:"remote_dir"` // <upload.remote_dir>/<release-id> on the server
	Files     int    `json:"files"`
	Bytes     int64  `json:"bytes"`
}

// VerifyCommand is what the person can run on the server to check the copy with the checksums that came along.
func (r *Result) VerifyCommand() string {
	return "cd " + r.RemoteDir + " && sha256sum -c checksums.sha256"
}

// Uploader runs scp attached to the terminal.
type Uploader struct {
	Run execx.Interactive
	Log *slog.Logger
}

// Command returns the working directory and the scp invocation for a request, after validating every part.
// scp runs inside the release's parent directory and is given the release's base name: a Windows path such as
// E:\dist\x would be read by scp as the host "E".
func Command(r Request) (dir string, args []string, err error) {
	switch {
	case !config.ValidUploadTarget(r.Target):
		return "", nil, fmt.Errorf("invalid upload target %q", r.Target)
	case !config.ValidUploadDir(r.RemoteDir):
		return "", nil, fmt.Errorf("invalid upload directory %q", r.RemoteDir)
	case !config.ValidReleaseLabel(r.ReleaseID):
		return "", nil, fmt.Errorf("invalid release id %q", r.ReleaseID)
	case filepath.Base(r.LocalDir) != r.ReleaseID:
		return "", nil, fmt.Errorf("%s is not the directory of release %s", r.LocalDir, r.ReleaseID)
	}
	args = []string{
		"-r",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "ConnectTimeout=15",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=4",
		"-o", "ClearAllForwardings=yes",
		"-o", "ForwardAgent=no",
		"-o", "ForwardX11=no",
		"--", r.ReleaseID, r.Target + ":" + r.RemoteDir + "/",
	}
	return filepath.Dir(r.LocalDir), args, nil
}

// Describe is the command line for display (dry runs).
func Describe(r Request) (string, error) {
	dir, args, err := Command(r)
	if err != nil {
		return "", err
	}
	s := "scp"
	for _, a := range args {
		s += " " + a
	}
	return "in " + dir + ": " + s, nil
}

// Upload verifies the release locally and copies it with scp.
func (u *Uploader) Upload(ctx context.Context, r Request) (*Result, error) {
	dir, args, err := Command(r)
	if err != nil {
		return nil, err
	}
	if err := artifact.Verify(r.LocalDir); err != nil {
		return nil, fmt.Errorf("local release is not valid: %w", err)
	}
	files, total, err := measure(r.LocalDir)
	if err != nil {
		return nil, err
	}

	u.Log.Info("uploading", "release", r.ReleaseID, "to", r.Target+":"+r.RemoteDir, "files", files, "bytes", total)
	if err := u.Run.RunInteractive(ctx, dir, "scp", args...); err != nil {
		var ee *execx.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("scp failed (exit code %d). Check that `ssh %s` works from this terminal, that %q exists on the server and that there is free space; "+
				"if the transfer was interrupted, the partial copy %s/%s is still on the server", ee.Code, r.Target, r.RemoteDir, r.RemoteDir, r.ReleaseID)
		}
		return nil, fmt.Errorf("could not run scp (is OpenSSH installed and in PATH?): %w", err)
	}
	return &Result{
		ReleaseID: r.ReleaseID, Target: r.Target, RemoteDir: path.Join(r.RemoteDir, r.ReleaseID), Files: files, Bytes: total,
	}, nil
}

func measure(dir string) (files int, total int64, err error) {
	err = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files++
		total += info.Size()
		return nil
	})
	return files, total, err
}
