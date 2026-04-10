package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// resolveFile looks for a file in the project override directory first,
// then falls back to the extracted embedded assets directory.
// overrideSubdir is the subdirectory under .cspace/ for the project override.
// assetsSubdir is the subdirectory under assetsDir for the fallback.
func resolveFile(projectRoot, assetsDir, overrideSubdir, assetsSubdir, name string) (string, bool) {
	override := filepath.Join(projectRoot, ".cspace", overrideSubdir, name)
	if _, err := os.Stat(override); err == nil {
		return override, true
	}

	fallback := filepath.Join(assetsDir, assetsSubdir, name)
	if _, err := os.Stat(fallback); err == nil {
		return fallback, true
	}

	return fallback, false
}

// ResolveTemplate resolves a template file, checking the project override
// directory first, then falling back to the extracted embedded assets.
//
// Resolution order:
//  1. $PROJECT_ROOT/.cspace/<name>
//  2. $ASSETS_DIR/templates/<name>
func ResolveTemplate(projectRoot, assetsDir, name string) (string, error) {
	// Templates have a special override path: .cspace/<name> (no subdirectory)
	override := filepath.Join(projectRoot, ".cspace", name)
	if _, err := os.Stat(override); err == nil {
		return override, nil
	}

	fallback := filepath.Join(assetsDir, "templates", name)
	if _, err := os.Stat(fallback); err == nil {
		return fallback, nil
	}

	return "", fmt.Errorf("template not found: %s (checked %s and %s)", name, override, fallback)
}

// ResolveScript resolves a script file, checking the project override
// directory first, then falling back to the extracted embedded assets.
//
// Resolution order:
//  1. $PROJECT_ROOT/.cspace/scripts/<name>
//  2. $ASSETS_DIR/scripts/<name>
func ResolveScript(projectRoot, assetsDir, name string) string {
	path, _ := resolveFile(projectRoot, assetsDir, "scripts", "scripts", name)
	return path
}

// ResolveAgent resolves an agent playbook file, checking the project
// override directory first, then falling back to the extracted embedded assets.
//
// Resolution order:
//  1. $PROJECT_ROOT/.cspace/agents/<name>
//  2. $ASSETS_DIR/agents/<name>
func ResolveAgent(projectRoot, assetsDir, name string) string {
	path, _ := resolveFile(projectRoot, assetsDir, "agents", "agents", name)
	return path
}

// --- Config methods for convenience ---

// ResolveTemplate resolves a template file using this config's paths.
func (c *Config) ResolveTemplate(name string) (string, error) {
	return ResolveTemplate(c.ProjectRoot, c.AssetsDir, name)
}

// ResolveScript resolves a script file using this config's paths.
func (c *Config) ResolveScript(name string) string {
	return ResolveScript(c.ProjectRoot, c.AssetsDir, name)
}

// ResolveAgent resolves an agent playbook file using this config's paths.
func (c *Config) ResolveAgent(name string) string {
	return ResolveAgent(c.ProjectRoot, c.AssetsDir, name)
}
