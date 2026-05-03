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

const appPrefix = "cspace"

var gitRepoRe = regexp.MustCompile(`github\.com[:/](.+)$`)

// Config represents the merged cspace configuration.
type Config struct {
	Project    ProjectConfig            `json:"project"`
	Container  ContainerConfig          `json:"container"`
	Firewall   FirewallConfig           `json:"firewall"`
	Claude     ClaudeConfig             `json:"claude"`
	MCPServers map[string]interface{}   `json:"mcpServers,omitempty"`
	Verify     VerifyConfig             `json:"verify"`
	Agent      AgentConfig              `json:"agent"`
	Plugins    PluginsConfig            `json:"plugins"`
	Advisors   map[string]AdvisorConfig `json:"advisors,omitempty"`
	Services   string                   `json:"services"`
	PostSetup  string                   `json:"post_setup"`
	Resources  ResourcesConfig          `json:"resources,omitempty"`

	// ServiceURLs declares Traefik-routed project services whose URLs cspace
	// should inject into the main container as env vars. Key is the subdomain
	// label (matches the Traefik Host rule); value is a list of framework env
	// var names to alias to the same URL. cspace always exports
	// CSPACE_SERVICE_<LABEL>_URL, plus each alias (e.g. VITE_CONVEX_URL).
	ServiceURLs map[string][]string `json:"serviceUrls,omitempty"`

	// Runtime fields (not from JSON)
	ProjectRoot string `json:"-"`
	AssetsDir   string `json:"-"`
}

// AdvisorConfig configures a single long-running advisor agent.
// See docs/superpowers/specs/2026-04-18-advisor-agents-design.md.
type AdvisorConfig struct {
	Model            string `json:"model,omitempty"`
	Effort           string `json:"effort,omitempty"`
	SystemPromptFile string `json:"systemPromptFile,omitempty"`
	BaseBranch       string `json:"baseBranch,omitempty"`
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

// ResourcesConfig caps the substrate runtime allocation per sandbox.
// Zero values mean "use the substrate adapter's default" (Apple
// Container: 4 CPU / 4096 MiB) — projects only set the fields they
// want to deviate on.
type ResourcesConfig struct {
	CPUs      int `json:"cpus,omitempty"`
	MemoryMiB int `json:"memoryMiB,omitempty"`
}

// FirewallConfig controls the container network firewall.
type FirewallConfig struct {
	Enabled bool     `json:"enabled"`
	Domains []string `json:"domains"`
}

// ClaudeConfig holds Claude model settings.
type ClaudeConfig struct {
	Model            string `json:"model"`
	Effort           string `json:"effort"`
	CoordinatorModel string `json:"coordinatorModel,omitempty"`
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
	projectRoot, err := FindProjectRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("finding project root: %w", err)
	}

	defaultsBytes, err := assets.DefaultsJSON()
	if err != nil {
		return nil, fmt.Errorf("reading embedded defaults.json: %w", err)
	}

	var base map[string]interface{}
	if err := json.Unmarshal(defaultsBytes, &base); err != nil {
		return nil, fmt.Errorf("parsing defaults.json: %w", err)
	}

	// Merge project and local config files in precedence order
	for _, name := range []string{".cspace.json", ".cspace.local.json"} {
		data, err := os.ReadFile(filepath.Join(projectRoot, name))
		if err != nil {
			continue
		}
		var overlay map[string]interface{}
		if err := json.Unmarshal(data, &overlay); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", name, err)
		}
		base = DeepMerge(base, overlay)
	}

	// Apply environment variable overrides
	setNestedMapValue(base, "project", "name", os.Getenv("CSPACE_PROJECT_NAME"))
	setNestedMapValue(base, "project", "repo", os.Getenv("CSPACE_PROJECT_REPO"))

	// Convert merged map to Config struct via JSON round-trip
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

	cfg.autoDetect()

	return cfg, nil
}

// setNestedMapValue sets base[section][key] = value if value is non-empty,
// creating the section map if needed.
func setNestedMapValue(base map[string]interface{}, section, key, value string) {
	if value == "" {
		return
	}
	m, _ := base[section].(map[string]interface{})
	if m == nil {
		m = make(map[string]interface{})
		base[section] = m
	}
	m[key] = value
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

	for k, v := range base {
		result[k] = v
	}

	for k, overlayVal := range overlay {
		baseVal, exists := result[k]
		if !exists {
			result[k] = overlayVal
			continue
		}

		baseMap, baseIsMap := baseVal.(map[string]interface{})
		overlayMap, overlayIsMap := overlayVal.(map[string]interface{})
		if baseIsMap && overlayIsMap {
			result[k] = DeepMerge(baseMap, overlayMap)
		} else {
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
			return "", fmt.Errorf("not in a git repository (searched from %s)", absDir)
		}
		current = parent
	}
}

// autoDetect fills in empty project fields from the environment.
func (c *Config) autoDetect() {
	if c.Project.Name == "" {
		c.Project.Name = filepath.Base(c.ProjectRoot)
	}

	if c.Project.Repo == "" {
		c.Project.Repo = DetectGitRepo(c.ProjectRoot)
	}

	if c.Project.Prefix == "" && len(c.Project.Name) >= 2 {
		c.Project.Prefix = c.Project.Name[:2]
	} else if c.Project.Prefix == "" && len(c.Project.Name) > 0 {
		c.Project.Prefix = c.Project.Name
	}
}

// DetectGitRepo extracts the GitHub owner/repo from the git remote origin URL.
func DetectGitRepo(projectRoot string) string {
	cmd := exec.Command("git", "-C", projectRoot, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	url := strings.TrimSpace(string(out))
	matches := gitRepoRe.FindStringSubmatch(url)
	if len(matches) >= 2 {
		return strings.TrimSuffix(matches[1], ".git")
	}
	return ""
}

// --- Derived name helpers (matching config.sh) ---

// ComposeName returns the docker-compose project name for an instance.
func (c *Config) ComposeName(instance string) string {
	return c.Project.Prefix + "-" + instance
}

// ProjectNetwork returns the name of the shared Docker bridge network
// for this project. All devcontainer instances of the same project join
// this network, enabling inter-instance communication while keeping
// different projects fully isolated from each other.
func (c *Config) ProjectNetwork() string {
	return appPrefix + "-" + c.Project.Name
}

// ImageName returns the Docker image name for this project.
func (c *Config) ImageName() string {
	return appPrefix + "-" + c.Project.Name
}

// MemoryVolume returns the shared memory volume name.
func (c *Config) MemoryVolume() string {
	return appPrefix + "-" + c.Project.Name + "-memory"
}

// LogsVolume returns the shared logs volume name.
func (c *Config) LogsVolume() string {
	return appPrefix + "-" + c.Project.Name + "-logs"
}

// SessionsDir returns the host-side directory where this project's Claude
// Code session files (JSONL transcripts) are stored. Every container for
// the project bind-mounts this into /home/dev/.claude/projects/-workspace,
// so sessions persist across container rebuilds, survive volume wipes, and
// can be audited/resumed from any instance.
//
// Default: $HOME/.cspace/sessions/<project-name>. Overridable via
// CSPACE_SESSIONS_DIR for users with non-standard layouts.
func (c *Config) SessionsDir() string {
	if v := os.Getenv("CSPACE_SESSIONS_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("/tmp", "cspace-sessions", c.Project.Name)
	}
	return filepath.Join(home, ".cspace", "sessions", c.Project.Name)
}

// InstanceLabel returns the Docker label for this project's instances.
func (c *Config) InstanceLabel() string {
	return appPrefix + ".project=" + c.Project.Name
}

// IsInitialized returns true if a .cspace.json file exists in the project root.
func (c *Config) IsInitialized() bool {
	_, err := os.Stat(filepath.Join(c.ProjectRoot, ".cspace.json"))
	return err == nil
}
