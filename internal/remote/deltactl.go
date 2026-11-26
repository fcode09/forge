// Package remote runs a closed list of deltactl commands on the production host (repo itc-deploy).
// FORGE never sends anything else: instance, service, variable and tag are validated before they reach ssh.
package remote

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/fcode09/forge/internal/config"
	"github.com/fcode09/forge/internal/execx"
)

// Host runs argv on the production host. Run captures the output; RunLive also streams it to the terminal.
type Host interface {
	Run(ctx context.Context, argv ...string) (execx.Result, error)
	RunLive(ctx context.Context, argv ...string) (execx.Result, error)
}

var (
	instanceRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	serviceRe  = regexp.MustCompile(`^s[0-9]+$`)
)

// Deltactl is a handle on one deltactl instance of one host.
type Deltactl struct {
	Host     Host
	Path     string // absolute path of the deltactl script on the host
	Instance string
}

// New validates its arguments and returns a Deltactl.
func New(h Host, path, instance string) (*Deltactl, error) {
	if !instanceRe.MatchString(instance) {
		return nil, fmt.Errorf("invalid instance %q", instance)
	}
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return nil, fmt.Errorf("invalid deltactl path %q", path)
	}
	return &Deltactl{Host: h, Path: path, Instance: instance}, nil
}

// TagVariable returns the deltactl variable that holds the image tag of a service (s0 -> S0_TAG).
func TagVariable(service string) (string, error) {
	if !serviceRe.MatchString(service) {
		return "", fmt.Errorf("invalid service %q", service)
	}
	return strings.ToUpper(service) + "_TAG", nil
}

// Load runs `deltactl load <file>` for an image archive already staged on the host.
func (d *Deltactl) Load(ctx context.Context, file string) error {
	return d.live(ctx, "load "+file, "load", file)
}

// GetTag returns the tag configured for a service in the instance's .env.instance.
func (d *Deltactl) GetTag(ctx context.Context, service string) (string, error) {
	v, err := TagVariable(service)
	if err != nil {
		return "", err
	}
	res, err := d.Host.Run(ctx, d.Path, "env", d.Instance, "get", v)
	if err != nil {
		return "", wrap("env get "+v, res, err)
	}
	return strings.TrimSpace(res.Stdout), nil
}

// SetTag runs `deltactl env <instance> set <S?_TAG> <tag>`; deltactl validates the value before writing.
func (d *Deltactl) SetTag(ctx context.Context, service, tag string) error {
	v, err := TagVariable(service)
	if err != nil {
		return err
	}
	if tag == "latest" || !config.ValidReleaseLabel(tag) {
		return fmt.Errorf("invalid image tag %q", tag)
	}
	res, err := d.Host.Run(ctx, d.Path, "env", d.Instance, "set", v, tag)
	if err != nil {
		return wrap("env set "+v, res, err)
	}
	return nil
}

// Check runs `deltactl check <instance> <service>`: everything deploy requires, without starting anything.
func (d *Deltactl) Check(ctx context.Context, service string) error {
	if !serviceRe.MatchString(service) {
		return fmt.Errorf("invalid service %q", service)
	}
	return d.live(ctx, "check "+service, "check", d.Instance, service)
}

// Deploy runs `deltactl deploy <instance> <service>`, which starts the service and waits until it is healthy.
func (d *Deltactl) Deploy(ctx context.Context, service string) error {
	if !serviceRe.MatchString(service) {
		return fmt.Errorf("invalid service %q", service)
	}
	return d.live(ctx, "deploy "+service, "deploy", d.Instance, service)
}

// Rollback runs `deltactl rollback <instance> <service> --yes`.
func (d *Deltactl) Rollback(ctx context.Context, service string) error {
	if !serviceRe.MatchString(service) {
		return fmt.Errorf("invalid service %q", service)
	}
	return d.live(ctx, "rollback "+service, "rollback", d.Instance, service, "--yes")
}

// Status returns the raw output of `deltactl status <instance>`.
func (d *Deltactl) Status(ctx context.Context) (string, error) {
	res, err := d.Host.Run(ctx, d.Path, "status", d.Instance)
	if err != nil {
		return "", wrap("status", res, err)
	}
	return res.Stdout, nil
}

func (d *Deltactl) live(ctx context.Context, what string, args ...string) error {
	res, err := d.Host.RunLive(ctx, append([]string{d.Path}, args...)...)
	if err != nil {
		return wrap(what, res, err)
	}
	return nil
}

// Error is a deltactl command that failed on the host.
type Error struct {
	Command string
	Code    int
	Output  string
	Err     error
}

func (e *Error) Error() string {
	msg := "deltactl " + e.Command + " failed"
	if e.Code != 0 {
		msg += fmt.Sprintf(" (exit code %d)", e.Code)
	}
	if e.Output != "" {
		msg += ": " + e.Output
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

func wrap(what string, res execx.Result, err error) error {
	e := &Error{Command: what, Err: err}
	var ee *execx.ExitError
	if errors.As(err, &ee) {
		e.Code = ee.Code
	}
	out := strings.TrimSpace(res.Stderr)
	if out == "" {
		out = strings.TrimSpace(res.Stdout)
	}
	if lines := strings.Split(out, "\n"); len(lines) > 5 {
		out = strings.Join(lines[len(lines)-5:], "\n")
	}
	e.Output = out
	if out == "" && e.Code == 0 {
		e.Output = err.Error()
	}
	return e
}
