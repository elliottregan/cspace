//go:build darwin

package secrets

import (
	"os/exec"
	"testing"
)

func TestKeychainRoundtrip(t *testing.T) {
	const service = "cspace-test-roundtrip"
	const value = "test-value-123"

	t.Cleanup(func() {
		_ = exec.Command("security", "delete-generic-password", "-s", service).Run()
	})

	if err := WriteKeychain(service, value); err != nil {
		t.Skipf("Keychain write failed (likely no auth in test env): %v", err)
	}

	got, err := ReadKeychain(service)
	if err != nil {
		t.Fatalf("ReadKeychain: %v", err)
	}
	if got != value {
		t.Fatalf("got %q, want %q", got, value)
	}
}

func TestReadKeychainMissing(t *testing.T) {
	got, err := ReadKeychain("cspace-test-definitely-not-present-xyz")
	if err != nil {
		t.Fatalf("expected nil error for missing item, got: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty string for missing item, got: %q", got)
	}
}
