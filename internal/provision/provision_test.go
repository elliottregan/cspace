package provision

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateName(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"mercury", true},
		{"my-instance", true},
		{"my_instance", true},
		{"Instance123", true},
		{"a", true},
		{"ABC-def_123", true},
		{"", false},
		{"has space", false},
		{"has.dot", false},
		{"has/slash", false},
		{"has@at", false},
		{"name!", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateName(tt.name)
			if tt.valid && err != nil {
				t.Errorf("validateName(%q) returned error: %v, want nil", tt.name, err)
			}
			if !tt.valid && err == nil {
				t.Errorf("validateName(%q) returned nil, want error", tt.name)
			}
		})
	}
}

func TestGitRemoteURL(t *testing.T) {
	// Test the URL credential stripping logic directly
	tests := []struct {
		input    string
		expected string
	}{
		{"https://github.com/user/repo.git", "https://github.com/user/repo.git"},
		{"https://token@github.com/user/repo.git", "https://github.com/user/repo.git"},
		{"https://user:pass@github.com/user/repo.git", "https://github.com/user/repo.git"},
		{"git@github.com:user/repo.git", "git@github.com:user/repo.git"}, // SSH URL, no :// prefix
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := stripCredentials(tt.input)
			if result != tt.expected {
				t.Errorf("stripCredentials(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestEnsureTeleportDir(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "teleport")

	if err := ensureTeleportDir(dir); err != nil {
		t.Fatalf("ensureTeleportDir returned error: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat after ensure: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory, got mode %v", info.Mode())
	}

	// Second call must be idempotent.
	if err := ensureTeleportDir(dir); err != nil {
		t.Fatalf("second ensureTeleportDir call: %v", err)
	}
}
