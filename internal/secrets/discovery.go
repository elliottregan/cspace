// Package secrets provides host credential primitives: macOS Keychain reads
// and writes, and discovery of credentials the host already holds (the gh
// CLI's token, Claude Code's login blob).
//
// It makes no policy decisions. Which credential wins, which env var carries
// it, and whether it is still valid are all internal/credentials' concerns.
//
// SECURITY NOTE: credentials reach the substrate via process env (-e flags),
// which Apple Container's vminitd logs in full — anyone with `container logs`
// access on the host can read them.
package secrets

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

// DiscoverGhAuthToken returns the gh CLI's stored auth token via `gh auth token`.
// Empty string with nil error when gh isn't installed or the user isn't authed.
func DiscoverGhAuthToken() (string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", nil
	}
	cmd := exec.Command("gh", "auth", "token")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		// gh exits non-zero when not authed. That's a "no token" signal,
		// not an error worth propagating.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}
