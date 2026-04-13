package assets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsJSON_Parses(t *testing.T) {
	data, err := DefaultsJSON()
	if err != nil {
		t.Fatalf("DefaultsJSON() error: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("DefaultsJSON() returned empty data")
	}

	// Verify it's valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("DefaultsJSON() returned invalid JSON: %v", err)
	}

	// Check that expected top-level keys exist
	expectedKeys := []string{"project", "container", "firewall", "claude", "verify", "agent", "plugins"}
	for _, key := range expectedKeys {
		if _, ok := parsed[key]; !ok {
			t.Errorf("defaults.json missing expected key: %s", key)
		}
	}
}

func TestDefaultsJSON_FirewallEnabledTrue(t *testing.T) {
	data, err := DefaultsJSON()
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	fw := parsed["firewall"].(map[string]interface{})
	if fw["enabled"] != true {
		t.Errorf("expected firewall.enabled=true, got %v", fw["enabled"])
	}
}

func TestExtractTo_CreatesFiles(t *testing.T) {
	dir := t.TempDir()

	extractRoot, err := ExtractTo(dir, "test-version")
	if err != nil {
		t.Fatalf("ExtractTo() error: %v", err)
	}

	// Check key files exist
	keyFiles := []string{
		"defaults.json",
		"templates/Dockerfile",
		"templates/docker-compose.core.yml",
		"templates/docker-compose.shared.yml",
		"scripts/entrypoint.sh",
		"hooks/claude-progress-logger.sh",
		"agents/coordinator.md",
		"agents/implementer.md",
	}

	for _, f := range keyFiles {
		path := filepath.Join(extractRoot, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file to exist: %s", path)
		}
	}
}

func TestExtractTo_PreservesStructure(t *testing.T) {
	dir := t.TempDir()

	extractRoot, err := ExtractTo(dir, "test-version")
	if err != nil {
		t.Fatalf("ExtractTo() error: %v", err)
	}

	// Check directory structure
	expectedDirs := []string{
		"templates",
		"scripts",
		"hooks",
		"agents",
		"agent-supervisor",
		"skills",
	}

	for _, d := range expectedDirs {
		path := filepath.Join(extractRoot, d)
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			t.Errorf("expected directory to exist: %s", path)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", path)
		}
	}
}

func TestExtractTo_ShellScriptsExecutable(t *testing.T) {
	dir := t.TempDir()

	extractRoot, err := ExtractTo(dir, "test-version")
	if err != nil {
		t.Fatalf("ExtractTo() error: %v", err)
	}

	// Check that .sh files are executable
	shFiles := []string{
		"scripts/entrypoint.sh",
		"hooks/claude-progress-logger.sh",
	}

	for _, f := range shFiles {
		path := filepath.Join(extractRoot, f)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("stat %s: %v", path, err)
			continue
		}
		mode := info.Mode()
		if mode&0111 == 0 {
			t.Errorf("expected %s to be executable (mode=%v)", f, mode)
		}
	}
}

func TestExtractTo_VersionMarker(t *testing.T) {
	dir := t.TempDir()

	extractRoot, err := ExtractTo(dir, "v1.0.0")
	if err != nil {
		t.Fatalf("ExtractTo() error: %v", err)
	}

	// Check version marker
	markerPath := filepath.Join(extractRoot, ".version")
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("reading version marker: %v", err)
	}
	if string(data) != "v1.0.0\n" {
		t.Errorf("expected version marker=v1.0.0, got %q", string(data))
	}
}

func TestExtractTo_SkipsIfVersionMatches(t *testing.T) {
	dir := t.TempDir()

	// First extraction
	extractRoot, err := ExtractTo(dir, "v1.0.0")
	if err != nil {
		t.Fatalf("first ExtractTo() error: %v", err)
	}

	// Modify a file to verify it's NOT overwritten on re-extraction
	testFile := filepath.Join(extractRoot, "defaults.json")
	if err := os.WriteFile(testFile, []byte("modified"), 0644); err != nil {
		t.Fatal(err)
	}

	// Second extraction with same version — should skip
	_, err = ExtractTo(dir, "v1.0.0")
	if err != nil {
		t.Fatalf("second ExtractTo() error: %v", err)
	}

	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "modified" {
		t.Error("expected extraction to be skipped (file was overwritten)")
	}
}

func TestExtractTo_ReExtractsOnVersionChange(t *testing.T) {
	dir := t.TempDir()

	// First extraction
	extractRoot, err := ExtractTo(dir, "v1.0.0")
	if err != nil {
		t.Fatalf("first ExtractTo() error: %v", err)
	}

	// Modify a file
	testFile := filepath.Join(extractRoot, "defaults.json")
	if err := os.WriteFile(testFile, []byte("modified"), 0644); err != nil {
		t.Fatal(err)
	}

	// Second extraction with different version — should re-extract
	_, err = ExtractTo(dir, "v2.0.0")
	if err != nil {
		t.Fatalf("second ExtractTo() error: %v", err)
	}

	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "modified" {
		t.Error("expected re-extraction to overwrite modified file")
	}
}
