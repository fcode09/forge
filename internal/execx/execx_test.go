package execx

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRunCapturesOutput(t *testing.T) {
	res, err := Exec{}.Run(context.Background(), "echo", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Stdout) != "hello" {
		t.Fatalf("stdout = %q", res.Stdout)
	}
}

func TestRunDoesNotUseShell(t *testing.T) {
	res, err := Exec{}.Run(context.Background(), "echo", "a; echo b")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(res.Stdout) != "a; echo b" {
		t.Fatalf("argument was interpreted by a shell: %q", res.Stdout)
	}
}

func TestRunExitCode(t *testing.T) {
	res, err := Exec{}.Run(context.Background(), "sh", "-c", "exit 3")
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 3 || res.ExitCode != 3 {
		t.Fatalf("err=%v res=%+v", err, res)
	}
}

func TestRunMissingProgram(t *testing.T) {
	if _, err := (Exec{}).Run(context.Background(), "forge-no-such-binary"); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunTeesLiveOutputAndDir(t *testing.T) {
	var live bytes.Buffer
	dir := t.TempDir()
	res, err := Exec{Options: Options{Dir: dir, Stdout: &live}}.Run(context.Background(), "pwd")
	if err != nil {
		t.Fatal(err)
	}
	if live.String() != res.Stdout || !strings.Contains(res.Stdout, dir) {
		t.Fatalf("live=%q captured=%q", live.String(), res.Stdout)
	}
}
