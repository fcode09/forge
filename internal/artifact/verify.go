package artifact

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Verify checks a release directory the same way the server does with `sha256sum -c`, and a bit more:
//   - every path in checksums.sha256 is a safe relative path and the file is a regular file,
//   - every checksum matches,
//   - no file exists in the directory that checksums.sha256 does not list,
//   - every file in manifest.json is covered by a matching checksum.
//
// Nothing is read or written outside dir.
func Verify(dir string) error {
	raw, err := os.ReadFile(filepath.Join(dir, ChecksumsFile))
	if err != nil {
		return err
	}
	listed, err := parseChecksums(string(raw))
	if err != nil {
		return err
	}
	for path, want := range listed {
		full := filepath.Join(dir, filepath.FromSlash(path))
		st, err := os.Lstat(full)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("%s: not a regular file", path)
		}
		got, _, err := hashFile(full)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if got != want {
			return fmt.Errorf("%s: checksum mismatch", path)
		}
	}

	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel == ChecksumsFile {
			return nil
		}
		if _, ok := listed[rel]; !ok {
			return fmt.Errorf("%s is not listed in %s", rel, ChecksumsFile)
		}
		return nil
	})
	if err != nil {
		return err
	}

	mb, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return err
	}
	var m Manifest
	if err := json.Unmarshal(mb, &m); err != nil {
		return fmt.Errorf("%s: %w", ManifestFile, err)
	}
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%s: unsupported schema_version %d", ManifestFile, m.SchemaVersion)
	}
	for _, f := range m.Files {
		if err := SafeRelPath(f.Path); err != nil {
			return fmt.Errorf("%s: %w", ManifestFile, err)
		}
		if listed[f.Path] != f.SHA256 {
			return fmt.Errorf("%s: %s does not match %s", ManifestFile, f.Path, ChecksumsFile)
		}
	}
	return nil
}

func parseChecksums(s string) (map[string]string, error) {
	out := map[string]string{}
	for i, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if line == "" {
			return nil, errNoChecksums
		}
		sum, path, ok := strings.Cut(line, "  ")
		if !ok || len(sum) != 64 || strings.Trim(sum, "0123456789abcdef") != "" {
			return nil, fmt.Errorf("%s line %d: malformed", ChecksumsFile, i+1)
		}
		if err := SafeRelPath(path); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", ChecksumsFile, i+1, err)
		}
		if _, dup := out[path]; dup {
			return nil, fmt.Errorf("%s line %d: duplicate path %s", ChecksumsFile, i+1, path)
		}
		out[path] = sum
	}
	return out, nil
}

// ReadMetadata verifies dir and returns its metadata.json.
func ReadMetadata(dir string) (*Metadata, error) {
	if err := Verify(dir); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, MetadataFile))
	if err != nil {
		return nil, err
	}
	var m Metadata
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", MetadataFile, err)
	}
	if m.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%s: unsupported schema_version %d", MetadataFile, m.SchemaVersion)
	}
	for _, im := range m.Images {
		if err := SafeRelPath(im.File); err != nil {
			return nil, fmt.Errorf("%s: %w", MetadataFile, err)
		}
	}
	return &m, nil
}
