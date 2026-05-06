package devcontainer

import "testing"

func TestConfigZeroValueIsValid(t *testing.T) {
	c := Config{}
	if c.WorkspaceFolder() != "/workspace" {
		t.Fatalf("default workspaceFolder = %q, want /workspace", c.WorkspaceFolder())
	}
	if c.RemoteUser() != "dev" {
		t.Fatalf("default remoteUser = %q, want dev", c.RemoteUser())
	}
}
