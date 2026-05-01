//go:build darwin

package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ReadKeychain reads a generic password from the macOS Keychain.
// Service-name convention: "cspace-<KEY>" so e.g. ANTHROPIC_API_KEY
// resolves to service "cspace-ANTHROPIC_API_KEY".
// Returns "" with no error if the item is not present.
func ReadKeychain(serviceName string) (string, error) {
	cmd := exec.Command("security", "find-generic-password", "-s", serviceName, "-w")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// security exits 44 when the item is not in the keychain.
			if exitErr.ExitCode() == 44 || strings.Contains(stderr.String(), "could not be found") {
				return "", nil
			}
		}
		return "", fmt.Errorf("security find-generic-password %s: %w (%s)",
			serviceName, err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

// WriteKeychain writes a generic password to the macOS Keychain.
// Idempotent — `-U` updates the existing item if one already exists.
func WriteKeychain(serviceName, password string) error {
	cmd := exec.Command("security", "add-generic-password",
		"-s", serviceName, "-a", "cspace", "-w", password, "-U")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("security add-generic-password %s: %w (%s)",
			serviceName, err, out)
	}
	return nil
}
