// Package logging builds the slog logger used by FORGE: readable text by default, JSON on request.
package logging

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
)

// NewRunID returns a short random ID that is attached to every log line of one execution.
func NewRunID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(b)
}

// New returns a logger writing to w. verbose enables debug level.
func New(w io.Writer, asJSON, verbose bool, runID string) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if asJSON {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(h).With("run_id", runID)
}
