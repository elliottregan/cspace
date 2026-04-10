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
		{"CSPACE_CONTAINER_NAME", "mercury"},
		{"CSPACE_PROJECT_NAME", "myproject"},
		{"CSPACE_PREFIX", "mp"},
		{"CSPACE_IMAGE", "cspace-myproject"},
		{"CSPACE_MEMORY_VOLUME", "cspace-myproject-memory"},
		{"CSPACE_LOGS_VOLUME", "cspace-myproject-logs"},
		{"CSPACE_LABEL", "cspace.project=myproject"},
		{"CSPACE_HOME", "/home/user/.cspace"},
		{"HOST_PORT_DEV", "5173"},     // mercury = index 0
		{"HOST_PORT_PREVIEW", "4173"}, // mercury = index 0
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
