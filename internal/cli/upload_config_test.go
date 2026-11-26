package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const uploadOnlyYAML = `version: 1
project: {name: app}
build:
  images:
    - {name: backend, image: app}
upload:
  target: mpa
`

func TestDeltactlCommandsRefuseWithoutDeployment(t *testing.T) {
	for _, cmd := range []string{"deploy", "transfer", "rollback", "status"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "forge.yaml"), []byte(uploadOnlyYAML), 0o644); err != nil {
			t.Fatal(err)
		}
		a, _, errb := newApp(t, dir)
		if code := run(a, cmd); code != ExitError || !strings.Contains(errb.String(), "no deployment section") {
			t.Errorf("%s: code=%d err=%q", cmd, code, errb.String())
		}
	}
}

func TestDoctorSkipsRemoteWithoutDeployment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "forge.yaml"), []byte(uploadOnlyYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	a, out, errb := newApp(t, dir)
	if code := run(a, "doctor", "--remote"); code != ExitOK || !strings.Contains(out.String(), "no deployment section") {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), errb.String())
	}
	if strings.Contains(out.String(), "known_hosts") {
		t.Fatalf("known_hosts belongs to deployment: %q", out.String())
	}
}
