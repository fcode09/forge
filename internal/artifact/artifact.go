// Package artifact exports built images into a release directory and writes the manifest, metadata and
// SHA-256 checksums that the server verifies before loading anything.
//
//	<out>/<release-id>/
//	  images/<name>.tar.gz
//	  manifest.json
//	  metadata.json
//	  checksums.sha256
package artifact

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// SchemaVersion of manifest.json and metadata.json.
const SchemaVersion = 1

// File names inside a release directory.
const (
	ManifestFile  = "manifest.json"
	MetadataFile  = "metadata.json"
	ChecksumsFile = "checksums.sha256"
	ImagesDir     = "images"
)

// FileEntry is one file listed in the manifest.
type FileEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest lists the image archives of a release.
type Manifest struct {
	SchemaVersion int         `json:"schema_version"`
	ReleaseID     string      `json:"release_id"`
	Files         []FileEntry `json:"files"`
}

// ImageEntry describes one packaged image.
type ImageEntry struct {
	Name    string `json:"name"`
	Service string `json:"service"`
	Ref     string `json:"ref"`
	ImageID string `json:"image_id"`
	File    string `json:"file"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

// Metadata describes the release.
type Metadata struct {
	SchemaVersion int          `json:"schema_version"`
	Project       string       `json:"project"`
	ReleaseID     string       `json:"release_id"`
	Commit        string       `json:"commit"`
	CreatedUTC    string       `json:"created_utc"`
	ForgeVersion  string       `json:"forge_version"`
	Instance      string       `json:"instance"`
	Images        []ImageEntry `json:"images"`
}

// Packager exports images. Run answers `docker image inspect`; Stream carries `docker save` to disk.
type Packager struct {
	Run          execx.Runner
	Stream       execx.Streamer
	Log          *slog.Logger
	Now          func() time.Time
	ForgeVersion string
}

// Input is one packaging request.
type Input struct {
	Config    *config.Config
	ReleaseID string
	Commit    string
	OutRoot   string // directory that will contain <release-id>/
}

// Package writes the release directory and returns its path and metadata. An existing release directory is
// never overwritten: releases are immutable.
func (p *Packager) Package(ctx context.Context, in Input) (string, *Metadata, error) {
	if errs := in.Config.Validate(); len(errs) > 0 {
		return "", nil, fmt.Errorf("invalid configuration: %s", strings.Join(errs, "; "))
	}
	if !config.ValidReleaseLabel(in.ReleaseID) {
		return "", nil, fmt.Errorf("invalid release id %q", in.ReleaseID)
	}
	final := filepath.Join(in.OutRoot, in.ReleaseID)
	if _, err := os.Lstat(final); err == nil {
		return "", nil, fmt.Errorf("%s already exists: releases are immutable, remove it or use another release id", final)
	}
	if err := os.MkdirAll(in.OutRoot, 0o755); err != nil {
		return "", nil, err
	}
	tmp, err := os.MkdirTemp(in.OutRoot, ".partial-")
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }() // no-op after a successful rename

	if err := os.Mkdir(filepath.Join(tmp, ImagesDir), 0o755); err != nil {
		return "", nil, err
	}
	meta := &Metadata{
		SchemaVersion: SchemaVersion,
		Project:       in.Config.Project.Name,
		ReleaseID:     in.ReleaseID,
		Commit:        in.Commit,
		CreatedUTC:    p.now().UTC().Format(time.RFC3339),
		ForgeVersion:  p.ForgeVersion,
		Instance:      in.Config.Deployment.Instance,
	}
	for _, im := range in.Config.Build.Images {
		e, err := p.exportImage(ctx, tmp, im, in.ReleaseID)
		if err != nil {
			return "", nil, err
		}
		meta.Images = append(meta.Images, *e)
	}

	manifest := Manifest{SchemaVersion: SchemaVersion, ReleaseID: in.ReleaseID}
	for _, e := range meta.Images {
		manifest.Files = append(manifest.Files, FileEntry{Path: e.File, SHA256: e.SHA256, Size: e.Size})
	}
	if err := writeJSON(filepath.Join(tmp, ManifestFile), manifest); err != nil {
		return "", nil, err
	}
	if err := writeJSON(filepath.Join(tmp, MetadataFile), meta); err != nil {
		return "", nil, err
	}
	if err := writeChecksums(tmp, manifest); err != nil {
		return "", nil, err
	}
	if err := Verify(tmp); err != nil {
		return "", nil, fmt.Errorf("self-check of the new artifact failed: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return "", nil, err
	}
	p.Log.Info("artifact written", "dir", final, "images", len(meta.Images))
	return final, meta, nil
}

func (p *Packager) exportImage(ctx context.Context, dir string, im config.Image, releaseID string) (*ImageEntry, error) {
	ref := im.ImageTag(releaseID)
	res, err := p.Run.Run(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", ref)
	if err != nil {
		return nil, fmt.Errorf("image %s not found in Docker (run `forge build` first): %w", ref, err)
	}
	rel := ImagesDir + "/" + im.Name + ".tar.gz"
	f, err := os.OpenFile(filepath.Join(dir, filepath.FromSlash(rel)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	cw := &countWriter{w: io.MultiWriter(f, h)}
	gz := gzip.NewWriter(cw)
	p.Log.Info("exporting image", "image", ref, "file", rel)
	_, serr := p.Stream.Stream(ctx, gz, "docker", "save", ref)
	if err := firstErr(serr, gz.Close(), f.Close()); err != nil {
		return nil, fmt.Errorf("export %s: %w", ref, err)
	}
	return &ImageEntry{
		Name: im.Name, Service: im.Service, Ref: ref, ImageID: strings.TrimSpace(res.Stdout),
		File: rel, SHA256: hex.EncodeToString(h.Sum(nil)), Size: cw.n,
	}, nil
}

func writeChecksums(dir string, m Manifest) error {
	var b strings.Builder
	for _, f := range m.Files {
		fmt.Fprintf(&b, "%s  %s\n", f.SHA256, f.Path)
	}
	for _, name := range []string{ManifestFile, MetadataFile} {
		sum, _, err := hashFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s  %s\n", sum, name)
	}
	return os.WriteFile(filepath.Join(dir, ChecksumsFile), []byte(b.String()), 0o644)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func (p *Packager) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	c.n += int64(n)
	return n, err
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

var errNoChecksums = errors.New("checksums.sha256 is missing or empty")
