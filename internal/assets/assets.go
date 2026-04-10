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
// Files ending in .sh are made executable (0755).
// A .version marker file is written with the given version string;
// if the marker already matches, extraction is skipped.
func ExtractTo(dir string, version string) (string, error) {
	extractRoot := filepath.Join(dir, "lib")

	// Check version marker — skip extraction if versions match
	markerPath := filepath.Join(extractRoot, ".version")
	if existing, err := os.ReadFile(markerPath); err == nil {
		if strings.TrimSpace(string(existing)) == version {
			return extractRoot, nil
		}
	}

	// Walk the embedded FS and extract everything
	err := fs.WalkDir(EmbeddedFS, "embedded", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Strip the "embedded/" prefix to get the relative path
		relPath := strings.TrimPrefix(path, "embedded")
		relPath = strings.TrimPrefix(relPath, "/")
		if relPath == "" {
			return nil // root directory
		}

		targetPath := filepath.Join(extractRoot, relPath)

		if d.IsDir() {
			return os.MkdirAll(targetPath, 0755)
		}

		// Read the embedded file
		data, err := EmbeddedFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading embedded %s: %w", path, err)
		}

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return fmt.Errorf("creating directory for %s: %w", targetPath, err)
		}

		// Determine file mode — shell scripts get executable bit
		mode := os.FileMode(0644)
		if strings.HasSuffix(path, ".sh") || strings.HasSuffix(path, ".mjs") {
			mode = 0755
		}

		// Write the file
		if err := os.WriteFile(targetPath, data, mode); err != nil {
			return fmt.Errorf("writing %s: %w", targetPath, err)
		}

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("extracting embedded assets: %w", err)
	}

	// Write version marker
	if err := os.WriteFile(markerPath, []byte(version+"\n"), 0644); err != nil {
		return "", fmt.Errorf("writing version marker: %w", err)
	}

	return extractRoot, nil
}
