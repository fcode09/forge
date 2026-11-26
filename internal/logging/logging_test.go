package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunIDIsHexAndUnique(t *testing.T) {
	a, b := NewRunID(), NewRunID()
	if len(a) != 8 || a == b {
		t.Fatalf("a=%q b=%q", a, b)
	}
}

func TestLoggerCarriesRunIDAndLevel(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, true, false, "abc12345")
	l.Debug("hidden")
	l.Info("shown")
	out := buf.String()
	if strings.Contains(out, "hidden") || !strings.Contains(out, `"run_id":"abc12345"`) || !strings.Contains(out, "shown") {
		t.Fatalf("out=%s", out)
	}
	buf.Reset()
	New(&buf, false, true, "x").Debug("seen")
	if !strings.Contains(buf.String(), "seen") {
		t.Fatal("verbose should show debug")
	}
}
