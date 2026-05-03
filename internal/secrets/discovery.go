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
