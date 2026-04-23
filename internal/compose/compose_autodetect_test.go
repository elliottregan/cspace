package compose

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elliottregan/cspace/internal/config"
)

// buildTestConfig creates a minimal *config.Config pointing at the given
// project root and assets dir, with no project name auto-detection side effects.
func buildTestConfig(projectRoot, assetsDir, services string) *config.Config {
	return &config.Config{
		Project: config.ProjectConfig{
			Name:   "testproject",
			Prefix: "tp",
		},
		ProjectRoot: projectRoot,
		AssetsDir:   assetsDir,
		Services:    services,
	}
}

// TestComposeFilesAutoDetect verifies that ComposeFiles auto-detects
// .devcontainer/docker-compose.yml when no explicit services file is set.
func TestComposeFilesAutoDetect(t *testing.T) {
	projectRoot := t.TempDir()
	assetsDir := t.TempDir()

	// Create the core template in the assets dir
	templatesDir := filepath.Join(assetsDir, "templates")
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		t.Fatalf("creating templates dir: %v", err)
	}
	coreFile := filepath.Join(templatesDir, "docker-compose.core.yml")
	if err := os.WriteFile(coreFile, []byte("# core compose\n"), 0644); err != nil {
		t.Fatalf("writing core template: %v", err)
	}

	// Create .devcontainer/docker-compose.yml in the project root
	devcontainerDir := filepath.Join(projectRoot, ".devcontainer")
	if err := os.MkdirAll(devcontainerDir, 0755); err != nil {
		t.Fatalf("creating .devcontainer dir: %v", err)
	}
	autoFile := filepath.Join(devcontainerDir, "docker-compose.yml")
	// Auto-detect only fires when the devcontainer compose extends the
	// cspace service — otherwise it's a standalone devcontainer that
	// should not be layered onto cspace's stack.
	autoContent := "services:\n  cspace:\n    environment:\n      - FOO=bar\n"
	if err := os.WriteFile(autoFile, []byte(autoContent), 0644); err != nil {
		t.Fatalf("writing devcontainer compose: %v", err)
	}

	cfg := buildTestConfig(projectRoot, assetsDir, "")

	files, err := ComposeFiles("mercury", cfg)
	if err != nil {
		t.Fatalf("ComposeFiles returned error: %v", err)
	}

	// core + auto-detected devcontainer + generated project-paths override
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d: %v", len(files), files)
	}
	if files[0] != coreFile {
		t.Errorf("files[0] = %q, want %q", files[0], coreFile)
	}
	if files[1] != autoFile {
		t.Errorf("files[1] = %q, want %q", files[1], autoFile)
	}
}

// TestComposeFilesSkipsStandaloneDevcontainer verifies that auto-detection
// skips a devcontainer/docker-compose.yml that does not extend the cspace
// service. Standalone devcontainer compose files commonly carry relative
// paths (e.g. env_file: - ../.env) that compose can mis-resolve when layered
// onto a compose file from a different directory, and they're rarely
// meaningful to layer in anyway.
func TestComposeFilesSkipsStandaloneDevcontainer(t *testing.T) {
	projectRoot := t.TempDir()
	assetsDir := t.TempDir()

	templatesDir := filepath.Join(assetsDir, "templates")
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		t.Fatalf("creating templates dir: %v", err)
	}
	coreFile := filepath.Join(templatesDir, "docker-compose.core.yml")
	if err := os.WriteFile(coreFile, []byte("# core compose\n"), 0644); err != nil {
		t.Fatalf("writing core template: %v", err)
	}

	devcontainerDir := filepath.Join(projectRoot, ".devcontainer")
	if err := os.MkdirAll(devcontainerDir, 0755); err != nil {
		t.Fatalf("creating .devcontainer dir: %v", err)
	}
	autoFile := filepath.Join(devcontainerDir, "docker-compose.yml")
	standaloneContent := "services:\n  devcontainer:\n    build: .\n    env_file:\n      - ../.env\n"
	if err := os.WriteFile(autoFile, []byte(standaloneContent), 0644); err != nil {
		t.Fatalf("writing devcontainer compose: %v", err)
	}

	cfg := buildTestConfig(projectRoot, assetsDir, "")

	files, err := ComposeFiles("mercury", cfg)
	if err != nil {
		t.Fatalf("ComposeFiles returned error: %v", err)
	}

	// core + generated project-paths override (devcontainer skipped)
	if len(files) != 2 {
		t.Fatalf("expected 2 files (standalone devcontainer should be skipped), got %d: %v", len(files), files)
	}
	for _, f := range files {
		if f == autoFile {
			t.Errorf("unexpected devcontainer compose in files: %v", files)
		}
	}
}

// TestComposeFilesExplicitOverridesAutoDetect verifies that an explicit
// cfg.Services value is used instead of the auto-detected .devcontainer file.
func TestComposeFilesExplicitOverridesAutoDetect(t *testing.T) {
	projectRoot := t.TempDir()
	assetsDir := t.TempDir()

	// Create the core template
	templatesDir := filepath.Join(assetsDir, "templates")
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		t.Fatalf("creating templates dir: %v", err)
	}
	coreFile := filepath.Join(templatesDir, "docker-compose.core.yml")
	if err := os.WriteFile(coreFile, []byte("# core compose\n"), 0644); err != nil {
		t.Fatalf("writing core template: %v", err)
	}

	// Create the auto-detect candidate (should NOT be picked)
	devcontainerDir := filepath.Join(projectRoot, ".devcontainer")
	if err := os.MkdirAll(devcontainerDir, 0755); err != nil {
		t.Fatalf("creating .devcontainer dir: %v", err)
	}
	autoFile := filepath.Join(devcontainerDir, "docker-compose.yml")
	if err := os.WriteFile(autoFile, []byte("# devcontainer compose\n"), 0644); err != nil {
		t.Fatalf("writing devcontainer compose: %v", err)
	}

	// Create the explicit services file
	explicitFile := filepath.Join(projectRoot, "my-services.yml")
	if err := os.WriteFile(explicitFile, []byte("# explicit services\n"), 0644); err != nil {
		t.Fatalf("writing explicit services: %v", err)
	}

	cfg := buildTestConfig(projectRoot, assetsDir, "my-services.yml")

	files, err := ComposeFiles("mercury", cfg)
	if err != nil {
		t.Fatalf("ComposeFiles returned error: %v", err)
	}

	// core + explicit services + generated project-paths override
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d: %v", len(files), files)
	}
	if files[0] != coreFile {
		t.Errorf("files[0] = %q, want %q", files[0], coreFile)
	}
	if files[1] != explicitFile {
		t.Errorf("files[1] = %q, want %q (auto-detected file should not be used)", files[1], explicitFile)
	}
}

// TestComposeFilesNoAutoDetect verifies that when there is no .devcontainer/
// dir and no explicit services, only the core file is returned.
func TestComposeFilesNoAutoDetect(t *testing.T) {
	projectRoot := t.TempDir()
	assetsDir := t.TempDir()

	// Create the core template only — no .devcontainer, no services
	templatesDir := filepath.Join(assetsDir, "templates")
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		t.Fatalf("creating templates dir: %v", err)
	}
	coreFile := filepath.Join(templatesDir, "docker-compose.core.yml")
	if err := os.WriteFile(coreFile, []byte("# core compose\n"), 0644); err != nil {
		t.Fatalf("writing core template: %v", err)
	}

	cfg := buildTestConfig(projectRoot, assetsDir, "")

	files, err := ComposeFiles("mercury", cfg)
	if err != nil {
		t.Fatalf("ComposeFiles returned error: %v", err)
	}

	// core + generated project-paths override
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(files), files)
	}
	if files[0] != coreFile {
		t.Errorf("files[0] = %q, want %q", files[0], coreFile)
	}
}
