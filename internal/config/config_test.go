package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func valid(t *testing.T) *Config {
	t.Helper()
	c, err := Parse(strings.NewReader(string(Template)))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTemplateIsValidAndMatchesExample(t *testing.T) {
	if errs := valid(t).Validate(); len(errs) != 0 {
		t.Fatalf("template invalid: %v", errs)
	}
	ex, err := os.ReadFile("../../forge.yaml.example")
	if err != nil {
		t.Fatal(err)
	}
	if string(ex) != string(Template) {
		t.Fatal("internal/config/template.yaml differs from forge.yaml.example")
	}
}

func TestDefaultsApplied(t *testing.T) {
	c, err := Parse(strings.NewReader("version: 1\nbuild:\n  images:\n    - {name: a, service: s0, image: a}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Deployment.SSHPort != 22 || c.Retention.Releases != 5 || c.Build.Images[0].Dockerfile != "Dockerfile" ||
		c.Build.Images[0].Context != "." || c.Deployment.RemoteDir != DefaultRemoteDir {
		t.Fatalf("defaults not applied: %+v", c)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	if _, err := Parse(strings.NewReader("version: 1\nbogus: true\n")); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestEmptyFile(t *testing.T) {
	if _, err := Parse(strings.NewReader("")); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*Config){
		"version":                    func(c *Config) { c.Version = 2 },
		"project.name":               func(c *Config) { c.Project.Name = "Bad Name" },
		"build.images[0].service":    func(c *Config) { c.Build.Images[0].Service = "web" },
		"build.images[0].image":      func(c *Config) { c.Build.Images[0].Image = "app:latest" },
		"build.images[0].context":    func(c *Config) { c.Build.Images[0].Context = "../outside" },
		"build.images[0].dockerfile": func(c *Config) { c.Build.Images[0].Dockerfile = "/etc/passwd" },
		"build.images[0].target":     func(c *Config) { c.Build.Images[0].Target = "a b" },
		"build.tests[0]":             func(c *Config) { c.Build.Tests = [][]string{{}} },
		"deployment.host":            func(c *Config) { c.Deployment.Host = "-oProxyCommand=x" },
		"deployment.user":            func(c *Config) { c.Deployment.User = "Root;" },
		"deployment.ssh_port":        func(c *Config) { c.Deployment.SSHPort = 70000 },
		"deployment.remote_dir":      func(c *Config) { c.Deployment.RemoteDir = "srv/forge" },
		"deployment.deltactl_path":   func(c *Config) { c.Deployment.DeltactlPath = "/srv/../bin/x" },
		"deployment.instance":        func(c *Config) { c.Deployment.Instance = "a b" },
		"deployment.known_hosts":     func(c *Config) { c.Deployment.KnownHosts = " " },
		"retention.releases":         func(c *Config) { c.Retention.Releases = -1 },
	}
	for field, mutate := range cases {
		c := valid(t)
		mutate(c)
		errs := c.Validate()
		found := false
		for _, e := range errs {
			if strings.HasPrefix(e, field+":") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: expected an error, got %v", field, errs)
		}
	}
}

func TestDuplicates(t *testing.T) {
	c := valid(t)
	c.Build.Images = append(c.Build.Images, c.Build.Images[0])
	joined := strings.Join(c.Validate(), "\n")
	for _, want := range []string{"duplicate name", "duplicate service", "duplicate image"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %s", want, joined)
		}
	}
}

func TestFindFileWalksUp(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := FindFile(sub); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	want := filepath.Join(root, FileName)
	if err := os.WriteFile(want, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := FindFile(sub)
	if err != nil || got != want {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestImageTagAndReleaseLabel(t *testing.T) {
	if (Image{Image: "auth-backend"}).ImageTag("20261125T1-abc") != "auth-backend:20261125T1-abc" {
		t.Fatal("bad tag")
	}
	for _, bad := range []string{"", "a b", "../x", "-x", strings.Repeat("a", 129)} {
		if ValidReleaseLabel(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	if !ValidReleaseLabel("20261125T101500Z-3cb19d0") {
		t.Fatal("good label rejected")
	}
}
