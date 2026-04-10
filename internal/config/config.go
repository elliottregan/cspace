// Package config implements three-layer configuration loading and merging
// for cspace. It reads and deep-merges:
//
//	embedded/defaults.json → $PROJECT_ROOT/.cspace.json → $PROJECT_ROOT/.cspace.local.json
//
// After merging, it auto-detects project name, repo, and prefix from
// the directory name and git remote if not explicitly set.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/elliottregan/cspace/internal/assets"
)

// Config represents the merged cspace configuration.
type Config struct {
	Project    ProjectConfig          `json:"project"`
	Container  ContainerConfig        `json:"container"`
	Firewall   FirewallConfig         `json:"firewall"`
	Claude     ClaudeConfig           `json:"claude"`
	MCPServers map[string]interface{} `json:"mcpServers,omitempty"`
	Verify     VerifyConfig           `json:"verify"`
	Agent      AgentConfig            `json:"agent"`
	Plugins    PluginsConfig          `json:"plugins"`
	Services   string                 `json:"services"`
	PostSetup  string                 `json:"post_setup"`

	// Runtime fields (not from JSON)
	ProjectRoot string `json:"-"`
	AssetsDir   string `json:"-"`
}

// ProjectConfig holds project identification fields.
type ProjectConfig struct {
	Name   string `json:"name"`
	Repo   string `json:"repo"`
	Prefix string `json:"prefix"`
}

// ContainerConfig holds container-specific settings.
type ContainerConfig struct {
	Ports       map[string]string `json:"ports"`
	Environment map[string]string `json:"environment"`
	Packages    []string          `json:"packages"`
}

// FirewallConfig controls the container network firewall.
type FirewallConfig struct {
	Enabled bool     `json:"enabled"`
	Domains []string `json:"domains"`
}

// ClaudeConfig holds Claude model settings.
type ClaudeConfig struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

// VerifyConfig holds verification command paths.
type VerifyConfig struct {
	All string `json:"all"`
	E2E string `json:"e2e"`
}

// AgentConfig holds agent-related settings.
type AgentConfig struct {
	IssueLabel string `json:"issue_label"`
}

// PluginsConfig controls Claude plugin installation.
type PluginsConfig struct {
	Enabled bool     `json:"enabled"`
	Install []string `json:"install"`
}

// Load reads and merges configuration from all sources.
// The dir parameter is the starting directory for project root detection.
// If assetsDir is non-empty, it is stored on the returned Config for use
// by resolve functions.
func Load(dir string, assetsDir string) (*Config, error) {
	// Find project root
	projectRoot, err := FindProjectRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("finding project root: %w", err)
	}

	// Read embedded defaults.json into a generic map
	defaultsBytes, err := assets.DefaultsJSON()
	if err != nil {
		return nil, fmt.Errorf("reading embedded defaults.json: %w", err)
	}

	var base map[string]interface{}
	if err := json.Unmarshal(defaultsBytes, &base); err != nil {
		return nil, fmt.Errorf("parsing defaults.json: %w", err)
	}

	// Merge .cspace.json if it exists
	projectConfigPath := filepath.Join(projectRoot, ".cspace.json")
	if data, err := os.ReadFile(projectConfigPath); err == nil {
		var overlay map[string]interface{}
		if err := json.Unmarshal(data, &overlay); err != nil {
			return nil, fmt.Errorf("parsing .cspace.json: %w", err)
		}
		base = DeepMerge(base, overlay)
	}

	// Merge .cspace.local.json if it exists
	localConfigPath := filepath.Join(projectRoot, ".cspace.local.json")
	if data, err := os.ReadFile(localConfigPath); err == nil {
		var overlay map[string]interface{}
		if err := json.Unmarshal(data, &overlay); err != nil {
			return nil, fmt.Errorf("parsing .cspace.local.json: %w", err)
		}
		base = DeepMerge(base, overlay)
	}

	// Apply environment variable overrides
	if v := os.Getenv("CSPACE_PROJECT_NAME"); v != "" {
		project, _ := base["project"].(map[string]interface{})
		if project == nil {
			project = make(map[string]interface{})
			base["project"] = project
		}
		project["name"] = v
	}
	if v := os.Getenv("CSPACE_PROJECT_REPO"); v != "" {
		project, _ := base["project"].(map[string]interface{})
		if project == nil {
			project = make(map[string]interface{})
			base["project"] = project
		}
		project["repo"] = v
	}

	// Marshal the merged map back to JSON, then unmarshal into Config struct
	mergedBytes, err := json.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("marshaling merged config: %w", err)
	}

	cfg := &Config{}
	if err := json.Unmarshal(mergedBytes, cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling config into struct: %w", err)
	}

	cfg.ProjectRoot = projectRoot
	cfg.AssetsDir = assetsDir

	// Auto-detect empty fields
	if err := cfg.autoDetect(); err != nil {
		return nil, fmt.Errorf("auto-detecting config: %w", err)
	}

	return cfg, nil
}

// DeepMerge performs a recursive merge of overlay into base,
// matching jq's `*` operator semantics:
//   - Objects (maps) merge recursively
//   - Arrays and scalars from overlay replace base values
//   - Keys in base that are not in overlay are preserved
//
// Neither base nor overlay are modified; a new map is returned.
func DeepMerge(base, overlay map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(base))

	// Copy all base keys
	for k, v := range base {
		result[k] = v
	}

	// Merge overlay keys
	for k, overlayVal := range overlay {
		baseVal, exists := result[k]
		if !exists {
			// New key from overlay
			result[k] = overlayVal
			continue
		}

		// Both exist — check if both are maps (recursive merge)
		baseMap, baseIsMap := baseVal.(map[string]interface{})
		overlayMap, overlayIsMap := overlayVal.(map[string]interface{})
		if baseIsMap && overlayIsMap {
			result[k] = DeepMerge(baseMap, overlayMap)
		} else {
			// Scalar or array: overlay replaces base
			result[k] = overlayVal
		}
	}

	return result
}

// FindProjectRoot walks up from dir looking for a .git/ directory.
func FindProjectRoot(dir string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	current := absDir
	for {
		gitPath := filepath.Join(current, ".git")
		if info, err := os.Stat(gitPath); err == nil && info.IsDir() {
			return current, nil
		}

		parent := filepath.Dir(current)
		if parent == current {
			// Reached filesystem root
			return "", fmt.Errorf("not in a git repository (searched from %s)", absDir)
		}
		current = parent
	}
}

// autoDetect fills in empty project fields from the environment.
func (c *Config) autoDetect() error {
	// Auto-detect project name from directory
	if c.Project.Name == "" {
		c.Project.Name = filepath.Base(c.ProjectRoot)
	}

	// Auto-detect repo from git remote
	if c.Project.Repo == "" {
		c.Project.Repo = detectGitRepo(c.ProjectRoot)
	}

	// Auto-derive prefix from project name
	if c.Project.Prefix == "" && len(c.Project.Name) >= 2 {
		c.Project.Prefix = c.Project.Name[:2]
	} else if c.Project.Prefix == "" && len(c.Project.Name) > 0 {
		c.Project.Prefix = c.Project.Name
	}

	return nil
}

// detectGitRepo extracts the GitHub owner/repo from the git remote origin URL.
func detectGitRepo(projectRoot string) string {
	cmd := exec.Command("git", "-C", projectRoot, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	url := strings.TrimSpace(string(out))
	// Match github.com[:/]owner/repo(.git)?
	re := regexp.MustCompile(`github\.com[:/](.+?)(?:\.git)?$`)
	matches := re.FindStringSubmatch(url)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

// --- Derived name helpers (matching config.sh) ---

// ComposeName returns the docker-compose project name for an instance.
func (c *Config) ComposeName(instance string) string {
	return c.Project.Prefix + "-" + instance
}

// ImageName returns the Docker image name for this project.
func (c *Config) ImageName() string {
	return "cspace-" + c.Project.Name
}

// MemoryVolume returns the shared memory volume name.
func (c *Config) MemoryVolume() string {
	return "cspace-" + c.Project.Name + "-memory"
}

// LogsVolume returns the shared logs volume name.
func (c *Config) LogsVolume() string {
	return "cspace-" + c.Project.Name + "-logs"
}

// InstanceLabel returns the Docker label for this project's instances.
func (c *Config) InstanceLabel() string {
	return "cspace.project=" + c.Project.Name
}

// IsInitialized returns true if a .cspace.json file exists in the project root.
func (c *Config) IsInitialized() bool {
	_, err := os.Stat(filepath.Join(c.ProjectRoot, ".cspace.json"))
	return err == nil
}
