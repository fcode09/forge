package release

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// StateDir is the per-project directory for FORGE's local state. It is excluded from the Git dirty check.
const StateDir = ".forge"

const lastFile = "last-build.json"

// Last records the most recent successful build so `forge package` knows which release to export.
type Last struct {
	ReleaseID  string `json:"release_id"`
	Commit     string `json:"commit"`
	CreatedUTC string `json:"created_utc"`
}

// SaveLast writes the record atomically into <projectDir>/.forge/.
func SaveLast(projectDir string, l Last) error {
	dir := filepath.Join(projectDir, StateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".last-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, lastFile))
}

// LoadLast reads the record and validates its release ID.
func LoadLast(projectDir string) (Last, error) {
	b, err := os.ReadFile(filepath.Join(projectDir, StateDir, lastFile))
	if err != nil {
		if os.IsNotExist(err) {
			return Last{}, fmt.Errorf("no previous build recorded: run `forge build` or pass --release")
		}
		return Last{}, err
	}
	var l Last
	if err := json.Unmarshal(b, &l); err != nil {
		return Last{}, fmt.Errorf("%s: %w", lastFile, err)
	}
	if err := Validate(l.ReleaseID); err != nil {
		return Last{}, fmt.Errorf("%s: %w", lastFile, err)
	}
	return l, nil
}
