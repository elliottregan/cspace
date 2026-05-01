//go:build darwin

package secrets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

// DiscoverClaudeOauthToken reads the Claude Code-credentials Keychain entry
// (written by `claude /login`), parses the JSON envelope, and returns the
// accessToken. Empty string with nil error when the entry is missing.
//
// The token is an OAuth access token (sk-ant-oat-...) that Claude Code
// refreshes when the user runs claude on the host. cspace consumes this as
// a convenience layer; users wanting a stable long-lived credential should
// instead store an API key (sk-ant-api-...) via `cspace keychain init`.
func DiscoverClaudeOauthToken() (string, error) {
	user := os.Getenv("USER")
	if user == "" {
		return "", nil
	}
	cmd := exec.Command("security", "find-generic-password",
		"-s", "Claude Code-credentials", "-a", user, "-w")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.ExitCode() == 44 || strings.Contains(stderr.String(), "could not be found") {
				return "", nil
			}
		}
		return "", fmt.Errorf("security find-generic-password Claude Code-credentials: %w", err)
	}
	raw := strings.TrimSpace(stdout.String())
	if raw == "" {
		return "", nil
	}

	var envelope struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return "", fmt.Errorf("parse Claude Code-credentials JSON: %w", err)
	}
	return envelope.ClaudeAiOauth.AccessToken, nil
}
