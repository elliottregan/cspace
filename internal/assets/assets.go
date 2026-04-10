// Package assets provides access to embedded cspace library files
// (templates, scripts, hooks, agents, etc.) and utilities for
// extracting them to disk.
package assets

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// EmbeddedFS contains the entire embedded/ directory tree.
// The all: prefix ensures dotfiles and hidden directories are included.
//
//go:embed all:embedded
var EmbeddedFS embed.FS

// DefaultsJSON returns the raw bytes of the embedded defaults.json.
func DefaultsJSON() ([]byte, error) {
	return EmbeddedFS.ReadFile("embedded/defaults.json")
}

// ExtractTo extracts all embedded assets to the given directory,
// preserving the directory structure under embedded/.
// It returns the path to the extraction root directory.
//
// Files ending in .sh or .mjs are made executable (0755).
// A .version marker file is written with the given version string;
// if the marker already matches, extraction is skipped.
// Stale files from previous versions are cleaned by removing
// the extraction directory before re-extracting.
func ExtractTo(dir string, version string) (string, error) {
	extractRoot := filepath.Join(dir, "lib")

	// Skip extraction if version marker matches
	markerPath := filepath.Join(extractRoot, ".version")
	if existing, err := os.ReadFile(markerPath); err == nil {
		if strings.TrimSpace(string(existing)) == version {
			return extractRoot, nil
		}
	}

	// Clean stale files from previous version before re-extracting
	if err := os.RemoveAll(extractRoot); err != nil {
		return "", fmt.Errorf("cleaning stale assets: %w", err)
	}

	// Use fs.Sub to strip the "embedded/" prefix automatically
	subFS, err := fs.Sub(EmbeddedFS, "embedded")
	if err != nil {
		return "", fmt.Errorf("creating sub filesystem: %w", err)
	}

	err = fs.WalkDir(subFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}

		targetPath := filepath.Join(extractRoot, path)

		if d.IsDir() {
			return os.MkdirAll(targetPath, 0755)
		}

		data, err := fs.ReadFile(subFS, path)
		if err != nil {
			return fmt.Errorf("reading embedded %s: %w", path, err)
		}

		// Defensive: ensure parent exists (embed.FS may omit directory entries)
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return fmt.Errorf("creating directory for %s: %w", targetPath, err)
		}

		mode := os.FileMode(0644)
		if strings.HasSuffix(path, ".sh") || strings.HasSuffix(path, ".mjs") {
			mode = 0755
		}

		if err := os.WriteFile(targetPath, data, mode); err != nil {
			return fmt.Errorf("writing %s: %w", targetPath, err)
		}

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("extracting embedded assets: %w", err)
	}

	if err := os.WriteFile(markerPath, []byte(version+"\n"), 0644); err != nil {
		return "", fmt.Errorf("writing version marker: %w", err)
	}

	return extractRoot, nil
}
