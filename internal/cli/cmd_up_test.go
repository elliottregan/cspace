package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMaybeNudgeMissingAnthropicAuthFiresOnce verifies the nudge prints on
// the first call when no auth is reachable, then stays silent on subsequent
// calls because the sentinel file gates it.
func TestMaybeNudgeMissingAnthropicAuthFiresOnce(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	var buf bytes.Buffer
	env := map[string]string{}

	maybeNudgeMissingAnthropicAuth(&buf, env)
	if !strings.Contains(buf.String(), "cspace keychain init") {
		t.Errorf("first call should print nudge; got: %q", buf.String())
	}

	sentinel := filepath.Join(tmp, ".cspace", ".no-claude-auth-nudge-shown")
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("sentinel file not created: %v", err)
	}

	buf.Reset()
	maybeNudgeMissingAnthropicAuth(&buf, env)
	if buf.Len() != 0 {
		t.Errorf("second call should be silent; got: %q", buf.String())
	}
}

// TestMaybeNudgeSilentWhenAuthPresent verifies the nudge stays silent — and
// does NOT create the sentinel — when ANTHROPIC_API_KEY is reachable.
func TestMaybeNudgeSilentWhenAuthPresent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	var buf bytes.Buffer
	env := map[string]string{"ANTHROPIC_API_KEY": "sk-ant-api-something"}

	maybeNudgeMissingAnthropicAuth(&buf, env)
	if buf.Len() != 0 {
		t.Errorf("nudge should not print when auth present; got: %q", buf.String())
	}

	sentinel := filepath.Join(tmp, ".cspace", ".no-claude-auth-nudge-shown")
	if _, err := os.Stat(sentinel); err == nil {
		t.Errorf("sentinel should not be created when nudge silent")
	}
}

// TestMaybeNudgeMissingDnsInstallFiresOnce verifies the dns-install nudge
// prints on the first call, then stays silent on subsequent calls because
// the sentinel file gates it.
func TestMaybeNudgeMissingDnsInstallFiresOnce(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	var buf bytes.Buffer
	maybeNudgeMissingDnsInstall(&buf)
	if !strings.Contains(buf.String(), "cspace dns install") {
		t.Errorf("first call should print nudge; got: %q", buf.String())
	}

	sentinel := filepath.Join(tmp, ".cspace", ".no-dns-install-nudge-shown")
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("sentinel file not created: %v", err)
	}

	buf.Reset()
	maybeNudgeMissingDnsInstall(&buf)
	if buf.Len() != 0 {
		t.Errorf("second call should be silent; got: %q", buf.String())
	}
}
