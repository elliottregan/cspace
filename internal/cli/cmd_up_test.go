package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elliottregan/cspace/internal/compose/v2"
	"github.com/elliottregan/cspace/internal/devcontainer"
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

// TestResolveSandboxImage verifies the four-level precedence for sandbox image selection.
func TestResolveSandboxImage(t *testing.T) {
	ctx := context.Background()
	const def = "cspace:latest"

	t.Run("nil plan returns default", func(t *testing.T) {
		if got := resolveSandboxImage(ctx, nil, def); got != def {
			t.Errorf("got %q, want %q", got, def)
		}
	})

	t.Run("compose service image wins", func(t *testing.T) {
		plan := &devcontainer.Plan{
			Devcontainer: &devcontainer.Config{Image: "dc-image:1"},
			Compose: &v2.Project{
				Services: map[string]*v2.Service{
					"workspace": {Name: "workspace", Image: "compose-image:2"},
				},
			},
			Service: "workspace",
		}
		if got := resolveSandboxImage(ctx, plan, def); got != "compose-image:2" {
			t.Errorf("got %q, want compose-image:2", got)
		}
	})

	t.Run("devcontainer image used when no compose service image", func(t *testing.T) {
		plan := &devcontainer.Plan{
			Devcontainer: &devcontainer.Config{Image: "dc-image:1"},
		}
		if got := resolveSandboxImage(ctx, plan, def); got != "dc-image:1" {
			t.Errorf("got %q, want dc-image:1", got)
		}
	})

	t.Run("falls back to default when nothing specified", func(t *testing.T) {
		plan := &devcontainer.Plan{
			Devcontainer: &devcontainer.Config{},
		}
		if got := resolveSandboxImage(ctx, plan, def); got != def {
			t.Errorf("got %q, want %q", got, def)
		}
	})
}

// TestWriteExtractedEnv verifies that writeExtractedEnv produces a correctly
// shell-escaped KEY=value file and that embedded single quotes survive the
// round-trip.
func TestWriteExtractedEnv(t *testing.T) {
	tmp := t.TempDir()
	env := map[string]string{
		"SIMPLE_KEY":   "simple-value",
		"KEY_WITH_SQ":  "it's a value",
		"ANOTHER":      "abc",
	}
	if err := writeExtractedEnv(tmp, env); err != nil {
		t.Fatalf("writeExtractedEnv: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "extracted.env"))
	if err != nil {
		t.Fatalf("read extracted.env: %v", err)
	}
	content := string(data)
	// Check each key is present with single-quote wrapping.
	if !strings.Contains(content, "ANOTHER='abc'") {
		t.Errorf("expected ANOTHER='abc' in output; got:\n%s", content)
	}
	if !strings.Contains(content, `KEY_WITH_SQ='it'\''s a value'`) {
		t.Errorf("expected escaped single quote in output; got:\n%s", content)
	}
	if !strings.Contains(content, "SIMPLE_KEY='simple-value'") {
		t.Errorf("expected SIMPLE_KEY='simple-value' in output; got:\n%s", content)
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
