// Package build builds the images declared in forge.yaml with the Docker CLI and runs the configured tests.
// Every command is an argument vector: nothing goes through a shell.
package build

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fcode09/forge/internal/config"
	"github.com/fcode09/forge/internal/execx"
)

// Options controls one build.
type Options struct {
	ProjectDir string // absolute project root (where forge.yaml lives)
	ReleaseID  string // immutable tag for every image
	Commit     string // short commit SHA for the image labels; may be empty
	SkipTests  bool
	DryRun     bool // print what would run, execute nothing
	// DaemonChecked means the caller already verified that a Docker engine answers, so Run does not probe again.
	DaemonChecked bool
}

// ImageResult is one built image.
type ImageResult struct {
	Name     string        `json:"name"`
	Service  string        `json:"service"`
	Ref      string        `json:"ref"`
	Duration time.Duration `json:"duration_ns"`
}

// Result is the outcome of a build.
type Result struct {
	ReleaseID string        `json:"release_id"`
	Images    []ImageResult `json:"images"`
	TestsRun  int           `json:"tests_run"`
	DryRun    bool          `json:"dry_run"`
}

// Builder runs builds. RunnerIn returns a runner whose working directory is dir.
type Builder struct {
	RunnerIn func(dir string) execx.Runner
	Log      *slog.Logger
	Out      io.Writer // dry-run plan is written here
	Now      func() time.Time
}

// Error is a failed build step.
type Error struct {
	Step   string // "docker", "image <name>" or "test <argv0>"
	Code   int
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("%s failed", e.Step)
	if e.Code != 0 {
		msg += fmt.Sprintf(" (exit code %d)", e.Code)
	}
	if t := tail(e.Stderr, 10); t != "" {
		msg += ":\n" + t
	}
	if e.Err != nil && e.Stderr == "" {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

// Run builds every image, then runs the configured tests, stopping at the first failure.
func (b *Builder) Run(ctx context.Context, cfg *config.Config, o Options) (*Result, error) {
	if errs := cfg.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("invalid configuration: %s", strings.Join(errs, "; "))
	}
	res := &Result{ReleaseID: o.ReleaseID, DryRun: o.DryRun}
	run := b.RunnerIn(o.ProjectDir)

	if !o.DryRun && !o.DaemonChecked {
		if _, err := run.Run(ctx, "docker", "version", "--format", "{{.Server.Version}}"); err != nil {
			return nil, stepError("docker daemon", nil, err)
		}
	}

	for _, im := range cfg.Build.Images {
		argv, err := buildArgs(o, im)
		if err != nil {
			return nil, fmt.Errorf("image %s: %w", im.Name, err)
		}
		ref := im.ImageTag(o.ReleaseID)
		if o.DryRun {
			b.plan("docker", argv)
			res.Images = append(res.Images, ImageResult{Name: im.Name, Service: im.Service, Ref: ref})
			continue
		}
		b.Log.Info("building image", "image", ref, "service", im.Service)
		start := b.now()
		if r, err := run.Run(ctx, "docker", argv...); err != nil {
			return nil, stepError("image "+im.Name, &r, err)
		}
		d := b.now().Sub(start)
		b.Log.Info("image built", "image", ref, "duration", d.Round(time.Millisecond))
		res.Images = append(res.Images, ImageResult{Name: im.Name, Service: im.Service, Ref: ref, Duration: d})
	}

	if o.SkipTests {
		b.Log.Info("tests skipped by request")
		return res, nil
	}
	for _, argv := range cfg.Build.Tests {
		if o.DryRun {
			b.plan(argv[0], argv[1:])
			res.TestsRun++
			continue
		}
		b.Log.Info("running test command", "argv0", argv[0])
		if r, err := run.Run(ctx, argv[0], argv[1:]...); err != nil {
			return nil, stepError("test "+argv[0], &r, err)
		}
		res.TestsRun++
	}
	return res, nil
}

// buildArgs returns the argv of `docker build` for one image, after checking the paths stay inside the project.
func buildArgs(o Options, im config.Image) ([]string, error) {
	ctxDir, err := inside(o.ProjectDir, im.Context)
	if err != nil {
		return nil, fmt.Errorf("context: %w", err)
	}
	if st, err := os.Stat(ctxDir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("context %q is not a directory", im.Context)
	}
	dockerfile, err := inside(o.ProjectDir, im.Dockerfile)
	if err != nil {
		return nil, fmt.Errorf("dockerfile: %w", err)
	}
	if st, err := os.Stat(dockerfile); err != nil || st.IsDir() {
		return nil, fmt.Errorf("dockerfile %q not found", im.Dockerfile)
	}
	args := []string{"build", "--file", dockerfile, "--tag", im.ImageTag(o.ReleaseID)}
	if im.Target != "" {
		args = append(args, "--target", im.Target)
	}
	args = append(args,
		"--label", "io.forge.release="+o.ReleaseID,
		"--label", "org.opencontainers.image.version="+o.ReleaseID)
	if o.Commit != "" {
		args = append(args, "--label", "org.opencontainers.image.revision="+o.Commit)
	}
	return append(args, ctxDir), nil
}

// inside joins rel to root and returns the real path, failing if it escapes root (including through symlinks).
func inside(root, rel string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	full := filepath.Join(realRoot, rel)
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%q does not exist", rel)
		}
		return "", err
	}
	r, err := filepath.Rel(realRoot, real)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%q resolves outside the project", rel)
	}
	return real, nil
}

func (b *Builder) plan(name string, args []string) {
	parts := append([]string{name}, args...)
	for i, p := range parts {
		parts[i] = quote(p)
	}
	fmt.Fprintln(b.Out, "would run:", strings.Join(parts, " "))
}

func (b *Builder) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

// quote is for display only; execution never goes through a shell.
func quote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@%+,{}", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func stepError(step string, r *execx.Result, err error) error {
	e := &Error{Step: step, Err: err}
	if r != nil {
		e.Code, e.Stderr = r.ExitCode, r.Stderr
	}
	return e
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return ""
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
