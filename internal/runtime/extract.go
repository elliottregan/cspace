// Package runtime manages cspace's bind-mountable runtime overlay.
// The overlay is extracted from cspace's embedded assets to
// ~/.cspace/runtime/<version>/ on first use, then bind-mounted into
// every microVM at /opt/cspace/. This decouples cspace runtime
// upgrades from project image rebuilds.
package runtime

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/assets"
)

type Manifest struct {
	Version     string    `json:"version"`
	ExtractedAt time.Time `json:"extractedAt"`
}

// Extract materializes the embedded runtime overlay tree to
// ~/.cspace/runtime/<version>/, returning the version and absolute path.
// Idempotent: a manifest matching the requested version short-circuits
// the copy.
func Extract(version string) (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	dest := filepath.Join(home, ".cspace", "runtime", version)
	manifestPath := filepath.Join(dest, "manifest.json")
	if data, err := os.ReadFile(manifestPath); err == nil {
		var m Manifest
		if json.Unmarshal(data, &m) == nil && m.Version == version {
			return version, dest, nil
		}
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", "", err
	}
	rfs, err := assets.RuntimeFS()
	if err != nil {
		return "", "", err
	}
	if err := copyFS(rfs, dest); err != nil {
		return "", "", fmt.Errorf("copy runtime tree: %w", err)
	}
	m := Manifest{Version: version, ExtractedAt: time.Now()}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		return "", "", err
	}
	return version, dest, nil
}

func copyFS(src fs.FS, dst string) error {
	return fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		target := filepath.Join(dst, path)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(src, path)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(path, ".sh") || strings.HasSuffix(path, ".mjs") {
			mode = 0o755
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, mode)
	})
}
