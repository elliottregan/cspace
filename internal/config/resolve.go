package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// ResolveTemplate resolves a template file, checking the project override
// directory first, then falling back to the extracted embedded assets.
//
// Resolution order:
//  1. $PROJECT_ROOT/.cspace/<name>
//  2. $ASSETS_DIR/templates/<name>
func ResolveTemplate(projectRoot, assetsDir, name string) (string, error) {
	// Check project override first
	override := filepath.Join(projectRoot, ".cspace", name)
	if _, err := os.Stat(override); err == nil {
		return override, nil
	}

	// Fall back to extracted assets
	defaultPath := filepath.Join(assetsDir, "templates", name)
	if _, err := os.Stat(defaultPath); err == nil {
		return defaultPath, nil
	}

	return "", fmt.Errorf("template not found: %s (checked %s and %s)", name, override, defaultPath)
}

// ResolveScript resolves a script file, checking the project override
// directory first, then falling back to the extracted embedded assets.
//
// Resolution order:
//  1. $PROJECT_ROOT/.cspace/scripts/<name>
//  2. $ASSETS_DIR/scripts/<name>
func ResolveScript(projectRoot, assetsDir, name string) string {
	// Check project override first
	override := filepath.Join(projectRoot, ".cspace", "scripts", name)
	if _, err := os.Stat(override); err == nil {
		return override
	}

	// Fall back to extracted assets (may not exist — matches bash behavior)
	return filepath.Join(assetsDir, "scripts", name)
}

// ResolveAgent resolves an agent playbook file, checking the project
// override directory first, then falling back to the extracted embedded assets.
//
// Resolution order:
//  1. $PROJECT_ROOT/.cspace/agents/<name>
//  2. $ASSETS_DIR/agents/<name>
func ResolveAgent(projectRoot, assetsDir, name string) string {
	// Check project override first
	override := filepath.Join(projectRoot, ".cspace", "agents", name)
	if _, err := os.Stat(override); err == nil {
		return override
	}

	// Fall back to extracted assets
	return filepath.Join(assetsDir, "agents", name)
}
