package credentials

import (
	"errors"
	"testing"
	"time"
)

// newTestHost returns a Host with every external dependency stubbed out.
// No test in this package touches the real Keychain, real gh, or the network.
func newTestHost() *Host {
	return &Host{
		ReadKeychain:    func(string) (string, error) { return "", nil },
		ReadSecretsFile: func(string) (map[string]string, error) { return nil, nil },
		LookupEnv:       func(string) (string, bool) { return "", false },
		DiscoverClaude:  func() (string, time.Time, error) { return "", time.Time{}, nil },
		DiscoverGh:      func() (string, error) { return "", nil },
	}
}

func TestResolveRanksProjectKeychainAboveGlobal(t *testing.T) {
	h := newTestHost()
	h.ReadKeychain = func(service string) (string, error) {
		switch service {
		case "cspace-resume-redux-GH_TOKEN":
			return "scoped", nil
		case "cspace-GH_TOKEN":
			return "global", nil
		}
		return "", nil
	}
	got := h.Resolve("resume-redux", "/p", "/home/u", nil)
	cands := got[KeyGHToken].Candidates
	if len(cands) != 2 {
		t.Fatalf("want 2 candidates, got %d: %+v", len(cands), cands)
	}
	if cands[0].Value != "scoped" || cands[0].Source != SourceProjectKeychain {
		t.Errorf("winner = %+v, want the project-scoped entry", cands[0])
	}
	if cands[1].Value != "global" || cands[1].Source != SourceGlobalKeychain {
		t.Errorf("fallback = %+v, want the global entry", cands[1])
	}
}

func TestResolveEnvFlagOutranksEverything(t *testing.T) {
	h := newTestHost()
	h.ReadKeychain = func(string) (string, error) { return "keychain", nil }
	h.LookupEnv = func(string) (string, bool) { return "ambient", true }
	h.DiscoverGh = func() (string, error) { return "discovered", nil }

	got := h.Resolve("proj", "/p", "/home/u", map[string]string{KeyGHToken: "flag"})
	w, ok := got[KeyGHToken].Winner()
	if !ok || w.Source != SourceEnvFlag || w.Value != "flag" {
		t.Fatalf("winner = %+v, want the --env value", w)
	}
}

func TestResolveAmbientShellRanksBelowKeychain(t *testing.T) {
	// Behavior change 2: os.Getenv("GH_TOKEN") used to win outright at
	// cmd_up.go:523, letting a stale export shadow a project-scoped token.
	h := newTestHost()
	h.ReadKeychain = func(service string) (string, error) {
		if service == "cspace-GH_TOKEN" {
			return "keychain", nil
		}
		return "", nil
	}
	h.LookupEnv = func(string) (string, bool) { return "ambient", true }

	w, _ := h.Resolve("proj", "/p", "/home/u", nil)[KeyGHToken].Winner()
	if w.Source != SourceGlobalKeychain {
		t.Fatalf("winner source = %v, want Keychain to outrank ambient shell", w.Source)
	}
}

func TestResolveKeychainOutranksSecretsFiles(t *testing.T) {
	// Behavior change 3: secrets.go:79-81 documented files winning over
	// Keychain as deliberate. Decision 1 reverses it on darwin.
	h := newTestHost()
	h.ReadKeychain = func(service string) (string, error) {
		if service == "cspace-GH_TOKEN" {
			return "keychain", nil
		}
		return "", nil
	}
	h.ReadSecretsFile = func(string) (map[string]string, error) {
		return map[string]string{KeyGHToken: "file"}, nil
	}
	w, _ := h.Resolve("proj", "/p", "/home/u", nil)[KeyGHToken].Winner()
	if w.Source != SourceGlobalKeychain {
		t.Fatalf("winner source = %v, want Keychain to outrank secrets.env", w.Source)
	}
}

func TestResolveProjectSecretsFileOutranksUserFile(t *testing.T) {
	h := newTestHost()
	h.ReadSecretsFile = func(path string) (map[string]string, error) {
		if path == "/p/.cspace/secrets.env" {
			return map[string]string{KeyGHToken: "project"}, nil
		}
		return map[string]string{KeyGHToken: "user"}, nil
	}
	w, _ := h.Resolve("proj", "/p", "/home/u", nil)[KeyGHToken].Winner()
	if w.Value != "project" || w.Source != SourceLegacyProjectFile {
		t.Fatalf("winner = %+v, want the project file to outrank the user file", w)
	}
}

func TestResolveCarriesOAuthExpiry(t *testing.T) {
	exp := time.Date(2026, 8, 17, 15, 57, 0, 0, time.UTC)
	h := newTestHost()
	h.DiscoverClaude = func() (string, time.Time, error) { return "sk-ant-oat01-x", exp, nil }

	w, ok := h.Resolve("proj", "/p", "/home/u", nil)[KeyClaudeOAuthToken].Winner()
	if !ok || !w.ExpiresAt.Equal(exp) {
		t.Fatalf("winner = %+v, want the discovered expiry carried through", w)
	}
}

func TestResolveAutoDiscoveryFillsOnlyTheOAuthCarrier(t *testing.T) {
	// autoDiscover historically filled only CLAUDE_CODE_OAUTH_TOKEN; the
	// Anthropic blob is always an sk-ant-oat access token. keychain status
	// claiming ANTHROPIC_API_KEY was auto-discovered described a state no
	// code path produced.
	h := newTestHost()
	h.DiscoverClaude = func() (string, time.Time, error) { return "sk-ant-oat01-x", time.Time{}, nil }
	got := h.Resolve("proj", "/p", "/home/u", nil)
	if len(got[KeyAnthropicAPIKey].Candidates) != 0 {
		t.Errorf("ANTHROPIC_API_KEY = %+v, want no auto-discovered candidate", got[KeyAnthropicAPIKey].Candidates)
	}
	if len(got[KeyClaudeOAuthToken].Candidates) != 1 {
		t.Errorf("CLAUDE_CODE_OAUTH_TOKEN = %+v, want the discovered token", got[KeyClaudeOAuthToken].Candidates)
	}
}

func TestResolveGhDiscoveryFillsAllThreeGitHubNames(t *testing.T) {
	h := newTestHost()
	h.DiscoverGh = func() (string, error) { return "gho_x", nil }
	got := h.Resolve("proj", "/p", "/home/u", nil)
	for _, k := range []string{KeyGHToken, KeyGitHubToken, KeyGitHubPAT} {
		if len(got[k].Candidates) != 1 || got[k].Candidates[0].Value != "gho_x" {
			t.Errorf("%s = %+v, want the discovered gh token", k, got[k].Candidates)
		}
	}
}

func TestResolveSkipsEmptyValues(t *testing.T) {
	h := newTestHost() // everything returns ""
	got := h.Resolve("proj", "/p", "/home/u", nil)
	for _, k := range Keys() {
		if len(got[k].Candidates) != 0 {
			t.Errorf("%s: want no candidates, got %+v", k, got[k].Candidates)
		}
	}
}

func TestResolveSkipsProjectKeychainWhenProjectUnknown(t *testing.T) {
	h := newTestHost()
	h.ReadKeychain = func(service string) (string, error) {
		if service == "cspace--GH_TOKEN" {
			t.Fatal("must not probe a project-scoped service with an empty project")
		}
		return "", nil
	}
	h.Resolve("", "/p", "/home/u", nil)
}

func TestResolveErrSurfacesLockedKeychain(t *testing.T) {
	// A miss is `security` exit 44 and ReadKeychain maps it to ("", nil).
	// A real error means the keychain is locked, and reporting that as
	// "not set" would make a locked keychain look empty.
	h := newTestHost()
	want := errors.New("keychain is locked")
	h.ReadKeychain = func(string) (string, error) { return "", want }

	if _, err := h.ResolveErr("proj", "/p", "/home/u", nil); !errors.Is(err, want) {
		t.Fatalf("ResolveErr() error = %v, want it to wrap %v", err, want)
	}
}

func TestResolveSwallowsKeychainErrorForConvenienceCallers(t *testing.T) {
	h := newTestHost()
	h.ReadKeychain = func(string) (string, error) { return "", errors.New("locked") }
	if got := h.Resolve("proj", "/p", "/home/u", nil); got == nil {
		t.Fatal("Resolve() should still return a map when ResolveErr fails")
	}
}
