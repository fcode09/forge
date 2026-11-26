// Package execx runs external programs with an argument vector and never through a shell.
package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

// Result is the outcome of one command.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Runner runs a program. It is an interface so callers can be tested without real tools.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (Result, error)
}

// Options adjusts how Exec runs a program.
type Options struct {
	Dir   string
	Stdin io.Reader
	// Stdout and Stderr, when set, receive the output live in addition to it being captured in the Result.
	Stdout, Stderr io.Writer
}

// Exec is the real Runner backed by os/exec.
type Exec struct{ Options Options }

// Run executes name with args. A non-zero exit code returns a Result and a non-nil *ExitError.
func (e Exec) Run(ctx context.Context, name string, args ...string) (Result, error) {
	return e.RunStdin(ctx, e.Options.Stdin, name, args...)
}

// StdinRunner is a Runner that can also feed the program's standard input.
type StdinRunner interface {
	Runner
	RunStdin(ctx context.Context, stdin io.Reader, name string, args ...string) (Result, error)
}

// RunStdin is Run with stdin as the program's standard input.
func (e Exec) RunStdin(ctx context.Context, stdin io.Reader, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = e.Options.Dir
	cmd.Stdin = stdin
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = tee(&out, e.Options.Stdout), tee(&errb, e.Options.Stderr)
	err := cmd.Run()
	res := Result{Stdout: out.String(), Stderr: errb.String()}
	if err == nil {
		return res, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		return res, &ExitError{Name: name, Code: res.ExitCode, Stderr: res.Stderr}
	}
	return res, fmt.Errorf("run %s: %w", name, err)
}

// ExitError reports a program that ran and exited with a non-zero code.
type ExitError struct {
	Name   string
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("%s exited with code %d", e.Name, e.Code)
}

func tee(capture *bytes.Buffer, live io.Writer) io.Writer {
	if live == nil {
		return capture
	}
	return io.MultiWriter(capture, live)
}

// Streamer runs a program and copies its stdout to a writer instead of buffering it, for large outputs
// such as `docker save`. Only stderr is captured.
type Streamer interface {
	Stream(ctx context.Context, stdout io.Writer, name string, args ...string) (stderr string, err error)
}

// Stream implements Streamer. A non-zero exit code returns an *ExitError.
func (e Exec) Stream(ctx context.Context, stdout io.Writer, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = e.Options.Dir
	cmd.Stdin = e.Options.Stdin
	var errb bytes.Buffer
	cmd.Stdout = stdout
	cmd.Stderr = tee(&errb, e.Options.Stderr)
	err := cmd.Run()
	if err == nil {
		return errb.String(), nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return errb.String(), &ExitError{Name: name, Code: ee.ExitCode(), Stderr: errb.String()}
	}
	return errb.String(), fmt.Errorf("run %s: %w", name, err)
}
