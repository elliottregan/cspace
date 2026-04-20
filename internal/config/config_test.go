package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDeepMerge_ObjectsMergeRecursively(t *testing.T) {
	base := map[string]interface{}{
		"project": map[string]interface{}{
			"name": "base-name",
			"repo": "base-repo",
		},
		"firewall": map[string]interface{}{
			"enabled": true,
			"domains": []interface{}{"example.com"},
		},
	}
	overlay := map[string]interface{}{
		"project": map[string]interface{}{
			"name": "overlay-name",
		},
	}

	result := DeepMerge(base, overlay)

	project := result["project"].(map[string]interface{})
	if project["name"] != "overlay-name" {
		t.Errorf("expected name=overlay-name, got %v", project["name"])
	}
	if project["repo"] != "base-repo" {
		t.Errorf("expected repo=base-repo, got %v", project["repo"])
	}

	// firewall should be untouched
	fw := result["firewall"].(map[string]interface{})
	if fw["enabled"] != true {
		t.Errorf("expected firewall.enabled=true, got %v", fw["enabled"])
	}
}

func TestDeepMerge_ArraysReplace(t *testing.T) {
	base := map[string]interface{}{
		"plugins": map[string]interface{}{
			"install": []interface{}{"a", "b", "c"},
		},
	}
	overlay := map[string]interface{}{
		"plugins": map[string]interface{}{
			"install": []interface{}{"x"},
		},
	}

	result := DeepMerge(base, overlay)

	plugins := result["plugins"].(map[string]interface{})
	install := plugins["install"].([]interface{})
	if len(install) != 1 || install[0] != "x" {
		t.Errorf("expected install=[x], got %v", install)
	}
}

func TestDeepMerge_ScalarsReplace(t *testing.T) {
	base := map[string]interface{}{
		"claude": map[string]interface{}{
			"model":  "old-model",
			"effort": "low",
		},
	}
	overlay := map[string]interface{}{
		"claude": map[string]interface{}{
			"model": "new-model",
		},
	}

	result := DeepMerge(base, overlay)

	claude := result["claude"].(map[string]interface{})
	if claude["model"] != "new-model" {
		t.Errorf("expected model=new-model, got %v", claude["model"])
	}
	if claude["effort"] != "low" {
		t.Errorf("expected effort=low (preserved from base), got %v", claude["effort"])
	}
}

func TestDeepMerge_OverlayKeysAdded(t *testing.T) {
	base := map[string]interface{}{
		"existing": "value",
	}
	overlay := map[string]interface{}{
		"new_key": "new_value",
	}

	result := DeepMerge(base, overlay)

	if result["existing"] != "value" {
		t.Errorf("expected existing=value, got %v", result["existing"])
	}
	if result["new_key"] != "new_value" {
		t.Errorf("expected new_key=new_value, got %v", result["new_key"])
	}
}

func TestDeepMerge_BaseKeysPreserved(t *testing.T) {
	base := map[string]interface{}{
		"keep_me":   "yes",
		"shared":    "base",
		"base_only": "present",
	}
	overlay := map[string]interface{}{
		"shared": "overlay",
	}

	result := DeepMerge(base, overlay)

	if result["keep_me"] != "yes" {
		t.Errorf("expected keep_me=yes, got %v", result["keep_me"])
	}
	if result["base_only"] != "present" {
		t.Errorf("expected base_only=present, got %v", result["base_only"])
	}
	if result["shared"] != "overlay" {
		t.Errorf("expected shared=overlay, got %v", result["shared"])
	}
}

func TestDeepMerge_BoolFalseOverridesTrue(t *testing.T) {
	base := map[string]interface{}{
		"firewall": map[string]interface{}{
			"enabled": true,
		},
	}
	overlay := map[string]interface{}{
		"firewall": map[string]interface{}{
			"enabled": false,
		},
	}

	result := DeepMerge(base, overlay)

	fw := result["firewall"].(map[string]interface{})
	if fw["enabled"] != false {
		t.Errorf("expected firewall.enabled=false, got %v", fw["enabled"])
	}
}

func TestDeepMerge_DoesNotMutateInputs(t *testing.T) {
	base := map[string]interface{}{
		"key": "base_value",
	}
	overlay := map[string]interface{}{
		"key": "overlay_value",
	}

	_ = DeepMerge(base, overlay)

	if base["key"] != "base_value" {
		t.Errorf("base was mutated: expected key=base_value, got %v", base["key"])
	}
	if overlay["key"] != "overlay_value" {
		t.Errorf("overlay was mutated: expected key=overlay_value, got %v", overlay["key"])
	}
}

// setupTestProject creates a minimal git repo with optional config files.
// It also clears the CSPACE_PROJECT_* env vars that Load treats as top-priority
// overrides, so tests running inside a cspace devcontainer (where these vars
// are always set) see the same behavior as tests in CI.
func setupTestProject(t *testing.T, projectConfig, localConfig string) string {
	t.Helper()
	t.Setenv("CSPACE_PROJECT_NAME", "")
	t.Setenv("CSPACE_PROJECT_REPO", "")
	dir := t.TempDir()

	// Create .git directory to make it look like a git repo
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	if projectConfig != "" {
		if err := os.WriteFile(filepath.Join(dir, ".cspace.json"), []byte(projectConfig), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if localConfig != "" {
		if err := os.WriteFile(filepath.Join(dir, ".cspace.local.json"), []byte(localConfig), 0644); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

func TestLoad_DefaultsOnly(t *testing.T) {
	dir := setupTestProject(t, "", "")

	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Firewall should be enabled by default
	if !cfg.Firewall.Enabled {
		t.Error("expected firewall.enabled=true from defaults")
	}

	// Claude model defaults to empty so each role can pick its own:
	// coordinator gets Sonnet (via coordinate.go fallback), workers fall back
	// to account default, advisors use their per-advisor model.
	if cfg.Claude.Model != "" {
		t.Errorf("expected model=<empty>, got %s", cfg.Claude.Model)
	}

	// Plugins should have the default list
	if len(cfg.Plugins.Install) == 0 {
		t.Error("expected non-empty plugins.install from defaults")
	}

	// Name should be auto-detected from directory
	if cfg.Project.Name == "" {
		t.Error("expected auto-detected project name")
	}
}

func TestLoad_WithProjectConfig(t *testing.T) {
	projectJSON := `{
		"project": {
			"name": "my-project",
			"repo": "user/my-project"
		},
		"claude": {
			"model": "custom-model"
		}
	}`

	dir := setupTestProject(t, projectJSON, "")

	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Project.Name != "my-project" {
		t.Errorf("expected name=my-project, got %s", cfg.Project.Name)
	}
	if cfg.Project.Repo != "user/my-project" {
		t.Errorf("expected repo=user/my-project, got %s", cfg.Project.Repo)
	}
	if cfg.Claude.Model != "custom-model" {
		t.Errorf("expected model=custom-model, got %s", cfg.Claude.Model)
	}

	// Effort comes from defaults — empty string means "use cspace's context-aware
	// default" (xhigh for container env, max for autonomous supervisor).
	if cfg.Claude.Effort != "" {
		t.Errorf("expected empty default effort, got %s", cfg.Claude.Effort)
	}
}

func TestLoad_ThreeLayer(t *testing.T) {
	projectJSON := `{
		"project": { "name": "proj" },
		"claude": { "model": "project-model" }
	}`
	localJSON := `{
		"claude": { "model": "local-model" }
	}`

	dir := setupTestProject(t, projectJSON, localJSON)

	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Local override should win
	if cfg.Claude.Model != "local-model" {
		t.Errorf("expected model=local-model (from local override), got %s", cfg.Claude.Model)
	}

	// Project name should come from project config
	if cfg.Project.Name != "proj" {
		t.Errorf("expected name=proj, got %s", cfg.Project.Name)
	}
}

func TestLoad_AutoDetectName(t *testing.T) {
	dir := setupTestProject(t, "", "")

	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Name should be auto-detected from directory basename
	expectedName := filepath.Base(dir)
	if cfg.Project.Name != expectedName {
		t.Errorf("expected name=%s, got %s", expectedName, cfg.Project.Name)
	}
}

func TestLoad_AutoDetectPrefix(t *testing.T) {
	projectJSON := `{"project": {"name": "myapp"}}`
	dir := setupTestProject(t, projectJSON, "")

	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Project.Prefix != "my" {
		t.Errorf("expected prefix=my, got %s", cfg.Project.Prefix)
	}
}

func TestLoad_EnvVarOverrides(t *testing.T) {
	dir := setupTestProject(t, "", "")

	t.Setenv("CSPACE_PROJECT_NAME", "env-name")
	t.Setenv("CSPACE_PROJECT_REPO", "env/repo")

	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Project.Name != "env-name" {
		t.Errorf("expected name=env-name, got %s", cfg.Project.Name)
	}
	if cfg.Project.Repo != "env/repo" {
		t.Errorf("expected repo=env/repo, got %s", cfg.Project.Repo)
	}
}

func TestLoad_ProjectRoot(t *testing.T) {
	dir := setupTestProject(t, "", "")

	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.ProjectRoot != dir {
		t.Errorf("expected ProjectRoot=%s, got %s", dir, cfg.ProjectRoot)
	}
}

func TestLoad_FirewallDisabledByProject(t *testing.T) {
	projectJSON := `{"firewall": {"enabled": false}}`
	dir := setupTestProject(t, projectJSON, "")

	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Firewall.Enabled {
		t.Error("expected firewall.enabled=false from project config")
	}
}

func TestLoad_MCPServersPassthrough(t *testing.T) {
	projectJSON := `{
		"mcpServers": {
			"my-server": {
				"command": "node",
				"args": ["server.js"],
				"env": {"KEY": "value"}
			}
		}
	}`
	dir := setupTestProject(t, projectJSON, "")

	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.MCPServers == nil {
		t.Fatal("expected mcpServers to be non-nil")
	}
	server, ok := cfg.MCPServers["my-server"]
	if !ok {
		t.Fatal("expected my-server in mcpServers")
	}

	serverMap, ok := server.(map[string]interface{})
	if !ok {
		t.Fatal("expected my-server to be a map")
	}
	if serverMap["command"] != "node" {
		t.Errorf("expected command=node, got %v", serverMap["command"])
	}
}

func TestLoad_ContainerPortsAndEnv(t *testing.T) {
	projectJSON := `{
		"container": {
			"ports": {"3000": "Frontend", "5432": "Database"},
			"environment": {"NODE_ENV": "development"}
		}
	}`
	dir := setupTestProject(t, projectJSON, "")

	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Container.Ports["3000"] != "Frontend" {
		t.Errorf("expected port 3000=Frontend, got %s", cfg.Container.Ports["3000"])
	}
	if cfg.Container.Environment["NODE_ENV"] != "development" {
		t.Errorf("expected NODE_ENV=development, got %s", cfg.Container.Environment["NODE_ENV"])
	}
}

func TestHelpers(t *testing.T) {
	cfg := &Config{
		Project: ProjectConfig{
			Name:   "myapp",
			Prefix: "ma",
		},
	}

	tests := []struct {
		name     string
		got      string
		expected string
	}{
		{"ComposeName", cfg.ComposeName("mercury"), "ma-mercury"},
		{"ImageName", cfg.ImageName(), "cspace-myapp"},
		{"MemoryVolume", cfg.MemoryVolume(), "cspace-myapp-memory"},
		{"LogsVolume", cfg.LogsVolume(), "cspace-myapp-logs"},
		{"InstanceLabel", cfg.InstanceLabel(), "cspace.project=myapp"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.got)
			}
		})
	}
}

func TestFindProjectRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create a subdirectory
	subdir := filepath.Join(dir, "sub", "deep")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	root, err := FindProjectRoot(subdir)
	if err != nil {
		t.Fatalf("FindProjectRoot() error: %v", err)
	}

	if root != dir {
		t.Errorf("expected root=%s, got %s", dir, root)
	}
}

func TestFindProjectRoot_NotInRepo(t *testing.T) {
	dir := t.TempDir()

	_, err := FindProjectRoot(dir)
	if err == nil {
		t.Error("expected error for directory without .git")
	}
}

func TestLoad_RealProjectConfig(t *testing.T) {
	// Test loading the actual repo's .cspace.json merged over defaults.json.
	// This verifies the real-world merge works correctly.
	repoRoot, err := FindProjectRoot(".")
	if err != nil {
		t.Fatalf("FindProjectRoot() error: %v", err)
	}
	cfg, err := Load(repoRoot, "")
	if err != nil {
		t.Fatalf("Load() with real project config error: %v", err)
	}

	if cfg.Project.Name != "cspace" {
		t.Errorf("expected name=cspace, got %s", cfg.Project.Name)
	}
	if cfg.Project.Repo != "elliottregan/cspace" {
		t.Errorf("expected repo=elliottregan/cspace, got %s", cfg.Project.Repo)
	}
	if cfg.Project.Prefix != "cs" {
		t.Errorf("expected prefix=cs, got %s", cfg.Project.Prefix)
	}

	// Verify defaults were applied for fields not in .cspace.json
	if cfg.Agent.IssueLabel != "ready" {
		t.Errorf("expected agent.issue_label=ready, got %s", cfg.Agent.IssueLabel)
	}

	// Verify plugins came from defaults (since .cspace.json doesn't override them)
	if len(cfg.Plugins.Install) == 0 {
		t.Error("expected plugins.install from defaults")
	}

	// Marshal to JSON to verify round-trip
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("JSON marshal error: %v", err)
	}
	t.Logf("Merged config:\n%s", string(data))
}

// loadConfigFromJSON is a test helper that creates a temporary project
// with the given JSON overlay in .cspace.json, then loads it.
func loadConfigFromJSON(t *testing.T, overlay string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".cspace.json"), []byte(overlay), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(dir, "")
}

func TestConfigAdvisorsBlock(t *testing.T) {
	cfg, err := loadConfigFromJSON(t, `{
		"advisors": {
			"decision-maker": {
				"model": "claude-opus-4-7",
				"effort": "max",
				"baseBranch": "main"
			},
			"custom": {
				"systemPromptFile": ".cspace/advisors/custom.md"
			}
		}
	}`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(cfg.Advisors) != 2 {
		t.Fatalf("want 2 advisors, got %d", len(cfg.Advisors))
	}
	dm, ok := cfg.Advisors["decision-maker"]
	if !ok {
		t.Fatalf("missing decision-maker entry")
	}
	if dm.Model != "claude-opus-4-7" {
		t.Errorf("Model = %q, want claude-opus-4-7", dm.Model)
	}
	if dm.Effort != "max" {
		t.Errorf("Effort = %q, want max", dm.Effort)
	}
	if dm.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q, want main", dm.BaseBranch)
	}
	custom := cfg.Advisors["custom"]
	if custom.SystemPromptFile != ".cspace/advisors/custom.md" {
		t.Errorf("SystemPromptFile = %q", custom.SystemPromptFile)
	}
}

func TestConfigAdvisorsNonNilAfterDefaults(t *testing.T) {
	cfg, err := loadConfigFromJSON(t, `{}`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Advisors == nil {
		t.Fatal("Advisors should be non-nil — defaults.json ships a decision-maker entry and DeepMerge preserves it when the overlay omits the key")
	}
}
