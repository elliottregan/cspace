package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v2 "github.com/elliottregan/cspace/internal/compose/v2"
	"github.com/elliottregan/cspace/internal/config"
	"github.com/elliottregan/cspace/internal/devcontainer"
	"github.com/spf13/cobra"
)

// TestImageIsStale covers the drift decision behind cspace up's rebuild offer:
// version match (with/without the goreleaser-stripped "v") is current; any
// drift or a missing label is stale.
func TestImageIsStale(t *testing.T) {
	cases := []struct {
		name       string
		imgVersion string
		hasLabel   bool
		cliVersion string
		want       bool
	}{
		{"exact match", "1.0.0-rc.29", true, "1.0.0-rc.29", false},
		{"match across v prefix", "v1.0.0-rc.29", true, "1.0.0-rc.29", false},
		{"drift", "1.0.0-rc.28", true, "1.0.0-rc.29", true},
		{"no label is stale", "", false, "1.0.0-rc.29", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := imageIsStale(tc.imgVersion, tc.hasLabel, tc.cliVersion); got != tc.want {
				t.Errorf("imageIsStale(%q, %v, %q) = %v, want %v",
					tc.imgVersion, tc.hasLabel, tc.cliVersion, got, tc.want)
			}
		})
	}
}

// TestPromptYesNo verifies the rebuild prompt parses replies and falls back to
// the default on an empty line, EOF, or unrecognized input.
func TestPromptYesNo(t *testing.T) {
	cases := []struct {
		name  string
		input string
		def   bool
		want  bool
	}{
		{"explicit y", "y\n", false, true},
		{"explicit yes", "yes\n", false, true},
		{"explicit n", "n\n", true, false},
		{"empty keeps default true", "\n", true, true},
		{"empty keeps default false", "\n", false, false},
		{"eof keeps default", "", true, true},
		{"unrecognized keeps default", "maybe\n", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.SetIn(strings.NewReader(tc.input))
			cmd.SetErr(&bytes.Buffer{})
			if got := promptYesNo(cmd, "Rebuild?", tc.def); got != tc.want {
				t.Errorf("promptYesNo(input=%q, def=%v) = %v, want %v",
					tc.input, tc.def, got, tc.want)
			}
		})
	}
}

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

func TestWarnExpiredAutoDiscoveredAuth(t *testing.T) {
	prev := discoverClaudeOauth
	t.Cleanup(func() { discoverClaudeOauth = prev })

	t.Run("prints fix hint when only credential is an expired auto-discovered token", func(t *testing.T) {
		discoverClaudeOauth = func() (string, time.Time, error) {
			return "sk-ant-oat-expired", time.Now().Add(-time.Hour), nil
		}
		var buf bytes.Buffer
		if !warnExpiredAutoDiscoveredAuth(&buf, map[string]string{}) {
			t.Fatal("expected warn to fire and return true")
		}
		out := buf.String()
		for _, want := range []string{"expired", "cspace keychain init", "will still boot"} {
			if !strings.Contains(out, want) {
				t.Errorf("message missing %q; got:\n%s", want, out)
			}
		}
	})

	t.Run("silent and does not consult keychain when a carrier is present", func(t *testing.T) {
		discoverClaudeOauth = func() (string, time.Time, error) {
			t.Fatal("discovery must not be consulted when a carrier is already present")
			return "", time.Time{}, nil
		}
		for _, carrier := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"} {
			var buf bytes.Buffer
			if warnExpiredAutoDiscoveredAuth(&buf, map[string]string{carrier: "present"}) {
				t.Errorf("%s present: expected no warning", carrier)
			}
			if buf.Len() != 0 {
				t.Errorf("%s present: expected no output; got %q", carrier, buf.String())
			}
		}
	})

	t.Run("silent for a still-valid token", func(t *testing.T) {
		discoverClaudeOauth = func() (string, time.Time, error) {
			return "sk-ant-oat-live", time.Now().Add(time.Hour), nil
		}
		var buf bytes.Buffer
		if warnExpiredAutoDiscoveredAuth(&buf, map[string]string{}) {
			t.Error("expected no warning for a still-valid token")
		}
	})

	t.Run("silent when no token is discoverable at all", func(t *testing.T) {
		discoverClaudeOauth = func() (string, time.Time, error) { return "", time.Time{}, nil }
		var buf bytes.Buffer
		if warnExpiredAutoDiscoveredAuth(&buf, map[string]string{}) {
			t.Error("expected no warning when nothing is discoverable")
		}
	})
}

// TestResolveBrowserEnabled covers the browser-sidecar precedence ladder:
// project CSPACE_BROWSER_CDP_URL > --no-browser > devcontainer tristate > default ON.
// TestWorkspaceHostSetWithoutBrowser guards against CSPACE_WORKSPACE_HOST
// regressing into a browser-sidecar-only assignment: agents/docs point at
// this var as THE address to reach the workspace, so it must be set even
// under --no-browser.
func TestWorkspaceHostSetWithoutBrowser(t *testing.T) {
	env := map[string]string{}
	applyWorkspaceHostEnv(env, "mercury", "resume-redux")
	if env["CSPACE_WORKSPACE_HOST"] != "mercury.resume-redux.cspace.test" {
		t.Fatalf("got %q", env["CSPACE_WORKSPACE_HOST"])
	}
}

func TestResolveBrowserEnabled(t *testing.T) {
	bptr := func(b bool) *bool { return &b }
	cases := []struct {
		name        string
		dcBrowser   *bool
		noBrowser   bool
		projectCDP  string
		wantEnabled bool
		reasonHas   string // substring expected in skipReason when disabled
	}{
		{"default on", nil, false, "", true, ""},
		{"devcontainer explicit false", bptr(false), false, "", false, "customizations.cspace.browser=false"},
		{"devcontainer explicit true", bptr(true), false, "", true, ""},
		{"--no-browser opt-out", nil, true, "", false, "--no-browser"},
		{"--no-browser overrides devcontainer true", bptr(true), true, "", false, "--no-browser"},
		{"project CDP url skips sidecar", nil, false, "ws://host:1234", false, "project-supplied"},
		{"project CDP url overrides devcontainer true", bptr(true), false, "ws://host:1234", false, "project-supplied"},
		{"project CDP url and --no-browser both", nil, true, "ws://host:1234", false, "--no-browser also set"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveBrowserEnabled(tc.dcBrowser, tc.noBrowser, tc.projectCDP)
			if got.enabled != tc.wantEnabled {
				t.Fatalf("enabled: got %v want %v", got.enabled, tc.wantEnabled)
			}
			if tc.wantEnabled && got.skipReason != "" {
				t.Errorf("enabled case must have empty skipReason, got %q", got.skipReason)
			}
			if !tc.wantEnabled && tc.reasonHas != "" && !strings.Contains(got.skipReason, tc.reasonHas) {
				t.Errorf("skipReason %q missing %q", got.skipReason, tc.reasonHas)
			}
		})
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
		"SIMPLE_KEY":  "simple-value",
		"KEY_WITH_SQ": "it's a value",
		"ANOTHER":     "abc",
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

// TestJoinPostCmd verifies the rendering of devcontainer postCreateCommand /
// postStartCommand from StringOrSlice into a shell-safe command line.
func TestJoinPostCmd(t *testing.T) {
	cases := []struct {
		name string
		in   devcontainer.StringOrSlice
		want string
	}{
		{"empty", nil, ""},
		{"single", devcontainer.StringOrSlice{"npm install"}, "npm install"},
		{"multi", devcontainer.StringOrSlice{"npm", "install"}, "'npm' 'install'"},
		{"single quote in arg", devcontainer.StringOrSlice{"echo", "it's fine"}, `'echo' 'it'"'"'s fine'`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := joinPostCmd(c.in)
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestResolveSharedBrowser(t *testing.T) {
	b := func(v bool) *bool { return &v }
	cases := []struct {
		name      string
		cfgShared *bool
		noShared  bool
		want      bool
	}{
		{"default shared", nil, false, true},
		{"config true", b(true), false, true},
		{"config false", b(false), false, false},
		{"flag off default", nil, true, false},
		{"flag overrides config true", b(true), true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveSharedBrowser(config.BrowserConfig{Shared: tc.cfgShared}, tc.noShared)
			if got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

// TestValidateSandboxName verifies the sandbox name validation rejects the
// reserved name "browser" and accepts other names like "issue-42".
func TestValidateSandboxName(t *testing.T) {
	cases := []struct {
		name    string
		project string
		input   string
		wantErr bool
		errHas  string // substring expected in error message when wantErr is true
	}{
		{"browser reserved", "test-project", "browser", true, "browser.test-project.cspace.test"},
		{"issue-42 allowed", "test-project", "issue-42", false, ""},
		{"mercury allowed", "test-project", "mercury", false, ""},
		{"custom-name allowed", "test-project", "custom-name", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSandboxName(tc.project, tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateSandboxName(%q, %q): got err=%v, wantErr=%v",
					tc.project, tc.input, err, tc.wantErr)
			}
			if tc.wantErr && err != nil && tc.errHas != "" {
				if !strings.Contains(err.Error(), tc.errHas) {
					t.Errorf("error message missing %q; got: %v", tc.errHas, err)
				}
			}
		})
	}
}
