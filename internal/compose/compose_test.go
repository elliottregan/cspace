package compose

import (
	"strings"
	"testing"

	"github.com/elliottregan/cspace/internal/config"
)

func TestComposeEnv(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{
			Name:   "myproject",
			Prefix: "mp",
			Repo:   "user/myproject",
		},
		Container: config.ContainerConfig{
			Environment: map[string]string{
				"NODE_ENV": "development",
			},
		},
		ProjectRoot: "/home/user/myproject",
		AssetsDir:   "/home/user/.cspace/lib",
	}

	env := ComposeEnv("mercury", cfg)

	// Build a lookup map
	m := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			m[parts[0]] = parts[1]
		}
	}

	tests := []struct {
		key  string
		want string
	}{
		{"COMPOSE_PROJECT_NAME", "mp-mercury"},
		{"CSPACE_CONTAINER_NAME", "mp-mercury"},
		{"CSPACE_PROJECT_NAME", "myproject"},
		{"CSPACE_PREFIX", "mp"},
		{"CSPACE_IMAGE", "cspace-myproject"},
		{"CSPACE_MEMORY_VOLUME", "cspace-myproject-memory"},
		{"CSPACE_LOGS_VOLUME", "cspace-myproject-logs"},
		{"CSPACE_LABEL", "cspace.project=myproject"},
		{"CSPACE_HOME", "/home/user/.cspace"},
		{"HOST_PORT_DEV", "0"},     // all instances use Docker-assigned ports
		{"HOST_PORT_PREVIEW", "0"}, // all instances use Docker-assigned ports
		{"PROJECT_ROOT", "/home/user/myproject"},
		{"CSPACE_ENV_NODE_ENV", "development"},
	}

	for _, tt := range tests {
		got, ok := m[tt.key]
		if !ok {
			t.Errorf("ComposeEnv missing key %q", tt.key)
			continue
		}
		if got != tt.want {
			t.Errorf("ComposeEnv[%q] = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestComposeEnvCustomName(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{
			Name:   "myproject",
			Prefix: "mp",
		},
		ProjectRoot: "/home/user/myproject",
		AssetsDir:   "/home/user/.cspace/lib",
	}

	env := ComposeEnv("feature-branch", cfg)

	m := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			m[parts[0]] = parts[1]
		}
	}

	// Custom name should get port 0 (Docker-assigned)
	if m["HOST_PORT_DEV"] != "0" {
		t.Errorf("HOST_PORT_DEV = %q, want %q", m["HOST_PORT_DEV"], "0")
	}
	if m["HOST_PORT_PREVIEW"] != "0" {
		t.Errorf("HOST_PORT_PREVIEW = %q, want %q", m["HOST_PORT_PREVIEW"], "0")
	}
	if m["COMPOSE_PROJECT_NAME"] != "mp-feature-branch" {
		t.Errorf("COMPOSE_PROJECT_NAME = %q, want %q", m["COMPOSE_PROJECT_NAME"], "mp-feature-branch")
	}
}

func TestComposeEnvProvisioningVars(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{
			Name:   "myproject",
			Prefix: "mp",
		},
		Firewall: config.FirewallConfig{
			Enabled: true,
			Domains: []string{"api.example.com", "cdn.example.com"},
		},
		Claude: config.ClaudeConfig{
			Model:  "claude-opus-4-6[1m]",
			Effort: "max",
		},
		MCPServers: map[string]interface{}{
			"test-server": map[string]interface{}{
				"command": "test",
			},
		},
		ProjectRoot: "/home/user/myproject",
		AssetsDir:   "/home/user/.cspace/lib",
	}

	env := ComposeEnv("mercury", cfg)

	m := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			m[parts[0]] = parts[1]
		}
	}

	// Firewall domains (space-separated)
	if got := m["CSPACE_FIREWALL_DOMAINS"]; got != "api.example.com cdn.example.com" {
		t.Errorf("CSPACE_FIREWALL_DOMAINS = %q, want %q", got, "api.example.com cdn.example.com")
	}

	// Claude model and effort
	if got := m["CSPACE_CLAUDE_MODEL"]; got != "claude-opus-4-6[1m]" {
		t.Errorf("CSPACE_CLAUDE_MODEL = %q, want %q", got, "claude-opus-4-6[1m]")
	}
	if got := m["CSPACE_CLAUDE_EFFORT"]; got != "max" {
		t.Errorf("CSPACE_CLAUDE_EFFORT = %q, want %q", got, "max")
	}

	// MCP servers (should be compact JSON)
	mcpVal, ok := m["CSPACE_MCP_SERVERS"]
	if !ok {
		t.Fatal("CSPACE_MCP_SERVERS not found in env")
	}
	if !strings.Contains(mcpVal, "test-server") {
		t.Errorf("CSPACE_MCP_SERVERS = %q, want to contain 'test-server'", mcpVal)
	}
}

func TestComposeEnvNoFirewallDomains(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{
			Name:   "myproject",
			Prefix: "mp",
		},
		Firewall: config.FirewallConfig{
			Enabled: true,
			Domains: nil,
		},
		Claude: config.ClaudeConfig{
			Model:  "sonnet",
			Effort: "high",
		},
		ProjectRoot: "/home/user/myproject",
		AssetsDir:   "/home/user/.cspace/lib",
	}

	env := ComposeEnv("earth", cfg)

	m := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			m[parts[0]] = parts[1]
		}
	}

	// No firewall domains should mean the key is absent
	if _, ok := m["CSPACE_FIREWALL_DOMAINS"]; ok {
		t.Error("CSPACE_FIREWALL_DOMAINS should not be set when domains list is empty")
	}

	// MCP servers should default to empty object (nil maps normalize to "{}")
	if got := m["CSPACE_MCP_SERVERS"]; got != "{}" {
		t.Errorf("CSPACE_MCP_SERVERS = %q, want %q", got, "{}")
	}
}
