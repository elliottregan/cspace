package sandboxmode

import "testing"

func TestIsInSandboxFalseByDefault(t *testing.T) {
	t.Setenv("CSPACE_SANDBOX_NAME", "")
	if IsInSandbox() {
		t.Fatal("expected false when CSPACE_SANDBOX_NAME is unset")
	}
}

func TestIsInSandboxTrueWhenSet(t *testing.T) {
	t.Setenv("CSPACE_SANDBOX_NAME", "p1")
	if !IsInSandbox() {
		t.Fatal("expected true when CSPACE_SANDBOX_NAME is set")
	}
}

func TestProjectFromEnv(t *testing.T) {
	t.Setenv("CSPACE_PROJECT", "myproj")
	t.Setenv("CSPACE_SANDBOX_NAME", "p1")
	if got := Project(); got != "myproj" {
		t.Fatalf("Project: got %q, want %q", got, "myproj")
	}
}

func TestRegistryURL(t *testing.T) {
	t.Setenv("CSPACE_REGISTRY_URL", "http://192.168.64.1:6280")
	if got := RegistryURL(); got != "http://192.168.64.1:6280" {
		t.Fatalf("RegistryURL: got %q", got)
	}
}
