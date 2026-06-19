package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseBasic(t *testing.T) {
	got, err := parse(strings.NewReader("# comment\nKEY1=value1\nKEY2=value2\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := map[string]string{"KEY1": "value1", "KEY2": "value2"}
	if !equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseQuoted(t *testing.T) {
	in := strings.NewReader(`KEY1="double quoted"
KEY2='single quoted'
KEY3=unquoted
KEY4=  trimmed  `)
	got, err := parse(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cases := map[string]string{
		"KEY1": "double quoted",
		"KEY2": "single quoted",
		"KEY3": "unquoted",
		"KEY4": "trimmed",
	}
	for k, want := range cases {
		if got[k] != want {
			t.Errorf("%s: got %q, want %q", k, got[k], want)
		}
	}
}

func TestParseSkipsBlanksAndComments(t *testing.T) {
	in := strings.NewReader("\n# header comment\nKEY1=value1\n\n  # indented comment\nKEY2=value2\n")
	got, err := parse(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 keys, got %d: %v", len(got), got)
	}
}

func TestLoadReturnsEmptyForMissingFiles(t *testing.T) {
	dir := t.TempDir()
	got, err := loadFromDirs(dir, dir)
	if err != nil {
		t.Fatalf("loadFromDirs: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
	}
}

func TestLoadProjectOverridesGlobal(t *testing.T) {
	globalDir := t.TempDir()
	projectDir := t.TempDir()

	mustWriteSecret(t, filepath.Join(globalDir, ".cspace", "secrets.env"),
		"GLOBAL_ONLY=g\nSHARED=from-global\n")
	mustWriteSecret(t, filepath.Join(projectDir, ".cspace", "secrets.env"),
		"PROJECT_ONLY=p\nSHARED=from-project\n")

	got, err := loadFromDirs(globalDir, projectDir)
	if err != nil {
		t.Fatalf("loadFromDirs: %v", err)
	}
	if got["GLOBAL_ONLY"] != "g" {
		t.Errorf("GLOBAL_ONLY: got %q, want %q", got["GLOBAL_ONLY"], "g")
	}
	if got["PROJECT_ONLY"] != "p" {
		t.Errorf("PROJECT_ONLY: got %q, want %q", got["PROJECT_ONLY"], "p")
	}
	if got["SHARED"] != "from-project" {
		t.Errorf("SHARED: project should override global; got %q", got["SHARED"])
	}
}

func TestAutoDiscoverFillsMissingKeys(t *testing.T) {
	out := map[string]string{}
	prev1, prev2 := discoverClaudeOauthToken, discoverGhAuthToken
	t.Cleanup(func() {
		discoverClaudeOauthToken = prev1
		discoverGhAuthToken = prev2
	})
	discoverClaudeOauthToken = func() (string, time.Time, error) { return "sk-ant-oat-stub", time.Time{}, nil }
	discoverGhAuthToken = func() (string, error) { return "gho_stub", nil }

	if err := autoDiscover(out); err != nil {
		t.Fatalf("autoDiscover: %v", err)
	}
	if out["CLAUDE_CODE_OAUTH_TOKEN"] != "sk-ant-oat-stub" {
		t.Errorf("CLAUDE_CODE_OAUTH_TOKEN: got %q, want %q", out["CLAUDE_CODE_OAUTH_TOKEN"], "sk-ant-oat-stub")
	}
	if out["GH_TOKEN"] != "gho_stub" {
		t.Errorf("GH_TOKEN: got %q, want %q", out["GH_TOKEN"], "gho_stub")
	}
	// Auto-discovery does not fill ANTHROPIC_API_KEY directly — that's the
	// alias propagation step's job (Task B).
	if _, present := out["ANTHROPIC_API_KEY"]; present {
		t.Errorf("ANTHROPIC_API_KEY: should not be filled by autoDiscover; got %q", out["ANTHROPIC_API_KEY"])
	}
}

func TestAutoDiscoverRefusesExpiredClaudeToken(t *testing.T) {
	out := map[string]string{}
	prev1, prev2, prevNow := discoverClaudeOauthToken, discoverGhAuthToken, timeNow
	t.Cleanup(func() {
		discoverClaudeOauthToken = prev1
		discoverGhAuthToken = prev2
		timeNow = prevNow
	})
	now := time.Unix(1_700_000_000, 0)
	timeNow = func() time.Time { return now }
	// Token whose access-token expiry lapsed an hour ago.
	discoverClaudeOauthToken = func() (string, time.Time, error) {
		return "sk-ant-oat-expired", now.Add(-time.Hour), nil
	}
	discoverGhAuthToken = func() (string, error) { return "", nil }

	if err := autoDiscover(out); err != nil {
		t.Fatalf("autoDiscover: %v", err)
	}
	if v, present := out["CLAUDE_CODE_OAUTH_TOKEN"]; present {
		t.Errorf("expired auto-discovered OAuth token must NOT be injected; got %q", v)
	}
}

func TestAutoDiscoverInjectsUnexpiredClaudeToken(t *testing.T) {
	prev1, prev2, prevNow := discoverClaudeOauthToken, discoverGhAuthToken, timeNow
	t.Cleanup(func() {
		discoverClaudeOauthToken = prev1
		discoverGhAuthToken = prev2
		timeNow = prevNow
	})
	now := time.Unix(1_700_000_000, 0)
	timeNow = func() time.Time { return now }
	discoverGhAuthToken = func() (string, error) { return "", nil }

	cases := map[string]time.Time{
		"future-expiry": now.Add(time.Hour), // still valid
		"zero-expiry":   {},                 // older Claude Code build: unknown expiry -> inject
	}
	for name, exp := range cases {
		t.Run(name, func(t *testing.T) {
			out := map[string]string{}
			discoverClaudeOauthToken = func() (string, time.Time, error) {
				return "sk-ant-oat-live", exp, nil
			}
			if err := autoDiscover(out); err != nil {
				t.Fatalf("autoDiscover: %v", err)
			}
			if out["CLAUDE_CODE_OAUTH_TOKEN"] != "sk-ant-oat-live" {
				t.Errorf("unexpired token should be injected; got %q", out["CLAUDE_CODE_OAUTH_TOKEN"])
			}
		})
	}
}

func TestAutoDiscoverDoesNotOverwriteExisting(t *testing.T) {
	out := map[string]string{
		"CLAUDE_CODE_OAUTH_TOKEN": "from-file",
		"GH_TOKEN":                "from-file-gh",
	}
	prev1, prev2 := discoverClaudeOauthToken, discoverGhAuthToken
	t.Cleanup(func() {
		discoverClaudeOauthToken = prev1
		discoverGhAuthToken = prev2
	})
	discoverClaudeOauthToken = func() (string, time.Time, error) {
		t.Errorf("discoverClaudeOauthToken should not be called when key is already set")
		return "", time.Time{}, nil
	}
	discoverGhAuthToken = func() (string, error) {
		t.Errorf("discoverGhAuthToken should not be called when key is already set")
		return "", nil
	}

	if err := autoDiscover(out); err != nil {
		t.Fatalf("autoDiscover: %v", err)
	}
	if out["CLAUDE_CODE_OAUTH_TOKEN"] != "from-file" {
		t.Errorf("CLAUDE_CODE_OAUTH_TOKEN: got %q, want %q", out["CLAUDE_CODE_OAUTH_TOKEN"], "from-file")
	}
	if out["GH_TOKEN"] != "from-file-gh" {
		t.Errorf("GH_TOKEN: got %q, want %q", out["GH_TOKEN"], "from-file-gh")
	}
}

func TestAutoDiscoverSkipsClaudeWhenAnthropicSet(t *testing.T) {
	out := map[string]string{"ANTHROPIC_API_KEY": "sk-ant-api-real"}
	prev1, prev2 := discoverClaudeOauthToken, discoverGhAuthToken
	t.Cleanup(func() {
		discoverClaudeOauthToken = prev1
		discoverGhAuthToken = prev2
	})
	discoverClaudeOauthToken = func() (string, time.Time, error) {
		t.Errorf("discoverClaudeOauthToken should not be called when ANTHROPIC_API_KEY is set")
		return "", time.Time{}, nil
	}
	discoverGhAuthToken = func() (string, error) { return "", nil }

	if err := autoDiscover(out); err != nil {
		t.Fatalf("autoDiscover: %v", err)
	}
	if _, present := out["CLAUDE_CODE_OAUTH_TOKEN"]; present {
		t.Errorf("CLAUDE_CODE_OAUTH_TOKEN should not be filled when ANTHROPIC_API_KEY is set")
	}
}

func TestAutoDiscoverSkipsGhWhenAnyGhAliasSet(t *testing.T) {
	cases := []string{"GH_TOKEN", "GITHUB_TOKEN", "GITHUB_PERSONAL_ACCESS_TOKEN"}
	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			out := map[string]string{key: "gh-from-file"}
			prev1, prev2 := discoverClaudeOauthToken, discoverGhAuthToken
			t.Cleanup(func() {
				discoverClaudeOauthToken = prev1
				discoverGhAuthToken = prev2
			})
			discoverClaudeOauthToken = func() (string, time.Time, error) { return "", time.Time{}, nil }
			discoverGhAuthToken = func() (string, error) {
				t.Errorf("discoverGhAuthToken should not be called when %s is set", key)
				return "", nil
			}

			if err := autoDiscover(out); err != nil {
				t.Fatalf("autoDiscover: %v", err)
			}
		})
	}
}

func TestNormalizeAnthropicCarrier(t *testing.T) {
	// (a) oat token misfiled in ANTHROPIC_API_KEY → moved to CLAUDE_CODE_OAUTH_TOKEN
	t.Run("oat-in-api-key-moved", func(t *testing.T) {
		m := map[string]string{"ANTHROPIC_API_KEY": "sk-ant-oat01-x"}
		normalizeAnthropicCarrier(m)
		if m["CLAUDE_CODE_OAUTH_TOKEN"] != "sk-ant-oat01-x" {
			t.Errorf("CLAUDE_CODE_OAUTH_TOKEN: got %q, want %q", m["CLAUDE_CODE_OAUTH_TOKEN"], "sk-ant-oat01-x")
		}
		if _, present := m["ANTHROPIC_API_KEY"]; present {
			t.Errorf("ANTHROPIC_API_KEY should be removed; got %q", m["ANTHROPIC_API_KEY"])
		}
	})

	// (b) api key misfiled in CLAUDE_CODE_OAUTH_TOKEN → moved to ANTHROPIC_API_KEY
	t.Run("api-key-in-oauth-token-moved", func(t *testing.T) {
		m := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-api03-x"}
		normalizeAnthropicCarrier(m)
		if m["ANTHROPIC_API_KEY"] != "sk-ant-api03-x" {
			t.Errorf("ANTHROPIC_API_KEY: got %q, want %q", m["ANTHROPIC_API_KEY"], "sk-ant-api03-x")
		}
		if _, present := m["CLAUDE_CODE_OAUTH_TOKEN"]; present {
			t.Errorf("CLAUDE_CODE_OAUTH_TOKEN should be removed; got %q", m["CLAUDE_CODE_OAUTH_TOKEN"])
		}
	})

	// (c) correctly-placed api key in ANTHROPIC_API_KEY is left untouched
	t.Run("api-key-correct-unchanged", func(t *testing.T) {
		m := map[string]string{"ANTHROPIC_API_KEY": "sk-ant-api03-y"}
		normalizeAnthropicCarrier(m)
		if m["ANTHROPIC_API_KEY"] != "sk-ant-api03-y" {
			t.Errorf("ANTHROPIC_API_KEY: got %q, want %q", m["ANTHROPIC_API_KEY"], "sk-ant-api03-y")
		}
		if _, present := m["CLAUDE_CODE_OAUTH_TOKEN"]; present {
			t.Errorf("CLAUDE_CODE_OAUTH_TOKEN should not be set; got %q", m["CLAUDE_CODE_OAUTH_TOKEN"])
		}
	})

	// (d) when destination is already set, source is dropped without clobbering
	t.Run("no-clobber-when-destination-set", func(t *testing.T) {
		m := map[string]string{
			"ANTHROPIC_API_KEY":       "sk-ant-oat01-wrong",
			"CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat01-correct",
		}
		normalizeAnthropicCarrier(m)
		// The oat value in ANTHROPIC_API_KEY should be dropped (not overwrite the real one).
		if m["CLAUDE_CODE_OAUTH_TOKEN"] != "sk-ant-oat01-correct" {
			t.Errorf("CLAUDE_CODE_OAUTH_TOKEN clobbered; got %q, want %q", m["CLAUDE_CODE_OAUTH_TOKEN"], "sk-ant-oat01-correct")
		}
		if _, present := m["ANTHROPIC_API_KEY"]; present {
			t.Errorf("ANTHROPIC_API_KEY should be removed; got %q", m["ANTHROPIC_API_KEY"])
		}
	})
}

func equal(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func mustWriteSecret(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
