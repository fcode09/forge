// Package config loads and validates forge.yaml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the name of the project configuration file.
const FileName = "forge.yaml"

// ErrNotFound is returned when no forge.yaml exists in the directory or any parent.
var ErrNotFound = errors.New("forge.yaml not found in this directory or any parent")

// Config is the content of forge.yaml.
type Config struct {
	Version    int        `yaml:"version"`
	Project    Project    `yaml:"project"`
	Build      Build      `yaml:"build"`
	Deployment Deployment `yaml:"deployment"`
	Retention  Retention  `yaml:"retention"`
}

// Project identifies the project being shipped.
type Project struct {
	Name string `yaml:"name"`
}

// Build lists the images to build and the tests to run first.
type Build struct {
	Images []Image    `yaml:"images"`
	Tests  [][]string `yaml:"tests"`
}

// Image is one image built from one Dockerfile and mapped to one deltactl service.
type Image struct {
	Name       string `yaml:"name"`
	Service    string `yaml:"service"`
	Context    string `yaml:"context"`
	Dockerfile string `yaml:"dockerfile"`
	Image      string `yaml:"image"`
	Target     string `yaml:"target"`
}

// Deployment describes the production host and the deltactl instance to update.
type Deployment struct {
	Host         string `yaml:"host"`
	User         string `yaml:"user"`
	SSHPort      int    `yaml:"ssh_port"`
	KnownHosts   string `yaml:"known_hosts"`
	RemoteDir    string `yaml:"remote_dir"`
	DeltactlPath string `yaml:"deltactl_path"`
	Instance     string `yaml:"instance"`
}

// Retention controls how many local releases are kept.
type Retention struct {
	Releases int `yaml:"releases"`
}

// Defaults applied when a field is omitted.
const (
	DefaultSSHPort    = 22
	DefaultKnownHosts = "~/.ssh/known_hosts"
	DefaultRemoteDir  = "/srv/forge"
	DefaultRetention  = 5
)

var (
	projectNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	serviceRe     = regexp.MustCompile(`^s[0-9]+$`)
	imageNameRe   = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*$`)
	targetRe      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	imageLabelRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	hostRe        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.:-]*$`)
	userRe        = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)
	instanceRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
)

// Parse decodes forge.yaml content strictly (unknown fields are errors) and applies defaults.
func Parse(r io.Reader) (*Config, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var c Config
	if err := dec.Decode(&c); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("forge.yaml is empty")
		}
		return nil, fmt.Errorf("parse forge.yaml: %w", err)
	}
	c.applyDefaults()
	return &c, nil
}

// Load reads and parses the file at path.
func Load(file string) (*Config, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	return Parse(bytes.NewReader(b))
}

func (c *Config) applyDefaults() {
	if c.Deployment.SSHPort == 0 {
		c.Deployment.SSHPort = DefaultSSHPort
	}
	if c.Deployment.KnownHosts == "" {
		c.Deployment.KnownHosts = DefaultKnownHosts
	}
	if c.Deployment.RemoteDir == "" {
		c.Deployment.RemoteDir = DefaultRemoteDir
	}
	if c.Retention.Releases == 0 {
		c.Retention.Releases = DefaultRetention
	}
	for i := range c.Build.Images {
		im := &c.Build.Images[i]
		if im.Context == "" {
			im.Context = "."
		}
		if im.Dockerfile == "" {
			im.Dockerfile = "Dockerfile"
		}
	}
}

// Validate returns every problem found, each prefixed with the field path. A nil slice means valid.
func (c *Config) Validate() []string {
	var errs []string
	add := func(field, msg string, a ...any) {
		errs = append(errs, field+": "+fmt.Sprintf(msg, a...))
	}

	if c.Version != 1 {
		add("version", "must be 1, got %d", c.Version)
	}
	if !projectNameRe.MatchString(c.Project.Name) {
		add("project.name", "required; lowercase letters, digits, '.', '_' and '-', starting with a letter or digit")
	}

	if len(c.Build.Images) == 0 {
		add("build.images", "at least one image is required")
	}
	names, services, images := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i, im := range c.Build.Images {
		p := fmt.Sprintf("build.images[%d]", i)
		if !projectNameRe.MatchString(im.Name) {
			add(p+".name", "required; lowercase letters, digits, '.', '_' and '-'")
		} else if names[im.Name] {
			add(p+".name", "duplicate name %q", im.Name)
		}
		names[im.Name] = true
		if !serviceRe.MatchString(im.Service) {
			add(p+".service", "must be a deltactl service key such as s0 or s1, got %q", im.Service)
		} else if services[im.Service] {
			add(p+".service", "duplicate service %q", im.Service)
		}
		services[im.Service] = true
		if !imageNameRe.MatchString(im.Image) {
			add(p+".image", "required; lowercase image name without registry tag or ':' (got %q)", im.Image)
		} else if images[im.Image] {
			add(p+".image", "duplicate image %q", im.Image)
		}
		images[im.Image] = true
		if err := relPath(im.Context); err != nil {
			add(p+".context", "%v", err)
		}
		if err := relPath(im.Dockerfile); err != nil {
			add(p+".dockerfile", "%v", err)
		}
		if im.Target != "" && !targetRe.MatchString(im.Target) {
			add(p+".target", "invalid build target %q", im.Target)
		}
	}
	for i, argv := range c.Build.Tests {
		if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
			add(fmt.Sprintf("build.tests[%d]", i), "must be a non-empty argv list, e.g. [\"npm\", \"test\"]")
		}
	}

	d := c.Deployment
	if !hostRe.MatchString(d.Host) {
		add("deployment.host", "required; a host name or IP address (got %q)", d.Host)
	}
	if !userRe.MatchString(d.User) {
		add("deployment.user", "required; a valid unix user name (got %q)", d.User)
	}
	if d.SSHPort < 1 || d.SSHPort > 65535 {
		add("deployment.ssh_port", "must be between 1 and 65535, got %d", d.SSHPort)
	}
	if err := absPath(d.RemoteDir); err != nil {
		add("deployment.remote_dir", "%v", err)
	}
	if err := absPath(d.DeltactlPath); err != nil {
		add("deployment.deltactl_path", "%v", err)
	}
	if !instanceRe.MatchString(d.Instance) {
		add("deployment.instance", "required; lowercase letters, digits, '_' and '-' (got %q)", d.Instance)
	}
	if strings.TrimSpace(d.KnownHosts) == "" {
		add("deployment.known_hosts", "must not be empty: SSH host verification cannot be disabled")
	}
	if c.Retention.Releases < 1 {
		add("retention.releases", "must be at least 1, got %d", c.Retention.Releases)
	}
	return errs
}

// ImageTag returns the immutable reference <image>:<releaseID> for an image.
func (im Image) ImageTag(releaseID string) string { return im.Image + ":" + releaseID }

// ValidReleaseLabel reports whether s is safe to use as a Docker tag and path element.
func ValidReleaseLabel(s string) bool { return len(s) <= 128 && imageLabelRe.MatchString(s) }

func relPath(p string) error {
	if p == "" {
		return errors.New("required")
	}
	if filepath.IsAbs(p) || path.IsAbs(filepath.ToSlash(p)) {
		return fmt.Errorf("must be relative to the project, got %q", p)
	}
	clean := path.Clean(filepath.ToSlash(p))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("must stay inside the project, got %q", p)
	}
	return nil
}

func absPath(p string) error {
	if p == "" {
		return errors.New("required")
	}
	if !path.IsAbs(p) {
		return fmt.Errorf("must be an absolute path, got %q", p)
	}
	if path.Clean(p) != p || strings.Contains(p, "..") {
		return fmt.Errorf("must be a clean path without '..', got %q", p)
	}
	if strings.ContainsAny(p, " \t\n'\"\\$`;&|<>*?") {
		return fmt.Errorf("contains characters that are not allowed, got %q", p)
	}
	return nil
}

// FindFile walks up from start looking for forge.yaml and returns its absolute path.
func FindFile(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, FileName)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNotFound
		}
		dir = parent
	}
}
