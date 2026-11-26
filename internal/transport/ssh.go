// Package transport talks to the production host through the system ssh binary, always with an argument
// vector, strict host key checking and no interactive prompts.
package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/fcode09/forge/internal/execx"
)

// Target is where and how to connect.
type Target struct {
	Host       string
	User       string
	Port       int
	KnownHosts string // ~/ is expanded
	SSHKey     string // optional; ~/ is expanded
}

// Client runs commands on the remote host over ssh.
type Client struct {
	Target  Target
	Exec    execx.StdinRunner
	Log     *slog.Logger
	HomeDir func() (string, error)
}

// safeArg is the only alphabet a remote argument may use. ssh hands the remote command to the login shell
// of the remote user, so every argument is restricted to characters the shell treats literally: there is
// no quoting, nothing is ever escaped, anything else is refused.
var safeArg = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// ErrUnsafeArg is returned for a remote argument outside the allowed alphabet.
var ErrUnsafeArg = errors.New("remote argument contains characters that are not allowed")

// RemoteCommand validates argv and returns the single string sent to ssh.
func RemoteCommand(argv []string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("empty remote command")
	}
	for _, a := range argv {
		if !safeArg.MatchString(a) {
			return "", fmt.Errorf("%w: %q", ErrUnsafeArg, a)
		}
	}
	return strings.Join(argv, " "), nil
}

// sshArgs builds the full ssh argument vector for a validated remote command.
func (c *Client) sshArgs(remote string) ([]string, error) {
	t := c.Target
	if t.Host == "" || strings.HasPrefix(t.Host, "-") || !safeArg.MatchString(t.Host) {
		return nil, fmt.Errorf("invalid ssh host %q", t.Host)
	}
	if t.User == "" || !safeArg.MatchString(t.User) || strings.HasPrefix(t.User, "-") {
		return nil, fmt.Errorf("invalid ssh user %q", t.User)
	}
	if t.Port < 1 || t.Port > 65535 {
		return nil, fmt.Errorf("invalid ssh port %d", t.Port)
	}
	kh, err := c.expand(t.KnownHosts)
	if err != nil {
		return nil, err
	}
	if kh == "" || strings.ContainsAny(kh, " \t\n") {
		return nil, fmt.Errorf("invalid known_hosts path %q", kh)
	}
	args := []string{
		"-p", strconv.Itoa(t.Port),
		"-l", t.User,
		"-T",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + kh,
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "PreferredAuthentications=publickey",
		"-o", "PasswordAuthentication=no",
		"-o", "KbdInteractiveAuthentication=no",
		"-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=4",
		"-o", "ClearAllForwardings=yes",
		"-o", "ForwardAgent=no",
		"-o", "ForwardX11=no",
		"-o", "LogLevel=ERROR",
	}
	if t.SSHKey != "" {
		key, err := c.expand(t.SSHKey)
		if err != nil {
			return nil, err
		}
		if strings.ContainsAny(key, " \t\n") {
			return nil, fmt.Errorf("invalid ssh key path %q", key)
		}
		args = append(args, "-i", key, "-o", "IdentitiesOnly=yes")
	}
	return append(args, "--", t.Host, remote), nil
}

func (c *Client) expand(p string) (string, error) {
	if strings.HasPrefix(p, "~/") {
		home, err := c.HomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		return home + p[1:], nil
	}
	return p, nil
}

// Run executes argv on the host. A non-zero remote exit code is returned as *execx.ExitError.
func (c *Client) Run(ctx context.Context, argv ...string) (execx.Result, error) {
	return c.RunStdin(ctx, nil, argv...)
}

// RunStdin is Run with stdin streamed to the remote program.
func (c *Client) RunStdin(ctx context.Context, stdin io.Reader, argv ...string) (execx.Result, error) {
	remote, err := RemoteCommand(argv)
	if err != nil {
		return execx.Result{}, err
	}
	args, err := c.sshArgs(remote)
	if err != nil {
		return execx.Result{}, err
	}
	c.Log.Debug("ssh", "host", c.Target.Host, "command", remote)
	return c.Exec.RunStdin(ctx, stdin, "ssh", args...)
}

// UploadFile copies a local file to remotePath by streaming it into `dd` on the host.
func (c *Client) UploadFile(ctx context.Context, local, remotePath string) error {
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()
	if res, err := c.RunStdin(ctx, f, "dd", "of="+remotePath, "bs=1M", "status=none"); err != nil {
		return fmt.Errorf("upload %s: %w%s", remotePath, err, stderrSuffix(res))
	}
	return nil
}

func stderrSuffix(r execx.Result) string {
	if s := strings.TrimSpace(r.Stderr); s != "" {
		return ": " + strings.SplitN(s, "\n", 2)[0]
	}
	return ""
}
