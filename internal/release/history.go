package release

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const historyFile = "history.jsonl"

// Event is one entry of the local release history (.forge/history.jsonl, one JSON object per line).
type Event struct {
	TimeUTC   string   `json:"time_utc"`
	Action    string   `json:"action"` // "deploy", "rollback" or "ship"
	ReleaseID string   `json:"release_id,omitempty"`
	Commit    string   `json:"commit,omitempty"`
	Instance  string   `json:"instance,omitempty"`
	Target    string   `json:"target,omitempty"` // upload target of a "ship"
	Services  []string `json:"services,omitempty"`
	Outcome   string   `json:"outcome"`
	Error     string   `json:"error,omitempty"`
}

// AppendEvent appends one event. The file is append-only: history is never rewritten.
func AppendEvent(projectDir string, e Event) error {
	dir := filepath.Join(projectDir, StateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, historyFile), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// ReadEvents returns the events oldest first. A missing file is an empty history; a damaged line is an error
// naming its line number.
func ReadEvents(projectDir string) ([]Event, error) {
	f, err := os.Open(filepath.Join(projectDir, StateDir, historyFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for n := 1; sc.Scan(); n++ {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", historyFile, n, err)
		}
		out = append(out, e)
	}
	return out, sc.Err()
}
