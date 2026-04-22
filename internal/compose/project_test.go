package compose

import (
	"strings"
	"testing"

	"github.com/elliottregan/cspace/internal/config"
)

func TestProjectStackEnv(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{
			Name:   "myproject",
			Prefix: "mp",
		},
		ProjectRoot: "/home/user/myproject",
		AssetsDir:   "/home/user/.cspace/lib",
	}

	env := ProjectStackEnv(cfg)

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
		{"COMPOSE_PROJECT_NAME", "cspace-myproject-stack"},
		{"CSPACE_PROJECT_STACK_NAME", "cspace-myproject-stack"},
		{"CSPACE_PROJECT_NAME", "myproject"},
		{"CSPACE_PROJECT_NETWORK", "cspace-myproject"},
		{"CSPACE_HOME", "/home/user/.cspace"},
	}

	for _, tt := range tests {
		got, ok := m[tt.key]
		if !ok {
			t.Errorf("ProjectStackEnv missing key %q", tt.key)
			continue
		}
		if got != tt.want {
			t.Errorf("ProjectStackEnv[%q] = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestProjectStackEnvMatchesInstanceEnv(t *testing.T) {
	// Verify that CSPACE_PROJECT_NETWORK and CSPACE_HOME are consistent
	// between the instance env and the project stack env.
	cfg := &config.Config{
		Project: config.ProjectConfig{
			Name:   "resume-redux",
			Prefix: "re",
		},
		ProjectRoot: "/home/user/resume-redux",
		AssetsDir:   "/home/user/.cspace/lib",
	}

	stackEnv := ProjectStackEnv(cfg)
	instanceEnv := ComposeEnv("mercury", cfg)

	stackMap := envToMap(stackEnv)
	instanceMap := envToMap(instanceEnv)

	// These keys must be identical across both envs
	shared := []string{"CSPACE_PROJECT_NETWORK", "CSPACE_HOME", "CSPACE_PROJECT_NAME"}
	for _, key := range shared {
		sv, sok := stackMap[key]
		iv, iok := instanceMap[key]
		if !sok {
			t.Errorf("ProjectStackEnv missing shared key %q", key)
			continue
		}
		if !iok {
			t.Errorf("ComposeEnv missing shared key %q", key)
			continue
		}
		if sv != iv {
			t.Errorf("shared key %q mismatch: stack=%q, instance=%q", key, sv, iv)
		}
	}

	// CSPACE_PROJECT_STACK_NAME should be present in both
	if _, ok := stackMap["CSPACE_PROJECT_STACK_NAME"]; !ok {
		t.Error("ProjectStackEnv missing CSPACE_PROJECT_STACK_NAME")
	}
	if _, ok := instanceMap["CSPACE_PROJECT_STACK_NAME"]; !ok {
		t.Error("ComposeEnv missing CSPACE_PROJECT_STACK_NAME")
	}
	if stackMap["CSPACE_PROJECT_STACK_NAME"] != instanceMap["CSPACE_PROJECT_STACK_NAME"] {
		t.Errorf("CSPACE_PROJECT_STACK_NAME mismatch: stack=%q, instance=%q",
			stackMap["CSPACE_PROJECT_STACK_NAME"], instanceMap["CSPACE_PROJECT_STACK_NAME"])
	}
}

func envToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			m[parts[0]] = parts[1]
		}
	}
	return m
}
