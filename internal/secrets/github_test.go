package secrets

import "testing"

func TestReconcileGitHubToken(t *testing.T) {
	origValidate := validateGitHubToken
	origDiscover := discoverGhAuthToken
	t.Cleanup(func() {
		validateGitHubToken = origValidate
		discoverGhAuthToken = origDiscover
	})

	t.Run("empty input is a no-op and never hits the network", func(t *testing.T) {
		validateGitHubToken = func(string) githubValidity {
			t.Fatal("empty token must not be validated")
			return githubUnknown
		}
		tok, warn := ReconcileGitHubToken("")
		if tok != "" || warn != "" {
			t.Fatalf("got (%q, %q), want empty", tok, warn)
		}
	})

	t.Run("valid token passes through with no fallback and no warning", func(t *testing.T) {
		validateGitHubToken = func(s string) githubValidity {
			if s == "good" {
				return githubValid
			}
			return githubInvalid
		}
		discoverGhAuthToken = func() (string, error) {
			t.Fatal("must not fall back when the token is valid")
			return "", nil
		}
		tok, warn := ReconcileGitHubToken("good")
		if tok != "good" || warn != "" {
			t.Fatalf("got (%q, %q), want (good, \"\")", tok, warn)
		}
	})

	t.Run("indeterminate result leaves the token untouched (never downgrade offline)", func(t *testing.T) {
		validateGitHubToken = func(string) githubValidity { return githubUnknown }
		discoverGhAuthToken = func() (string, error) {
			t.Fatal("must not fall back on an indeterminate check")
			return "", nil
		}
		tok, warn := ReconcileGitHubToken("maybe")
		if tok != "maybe" || warn != "" {
			t.Fatalf("got (%q, %q), want (maybe, \"\")", tok, warn)
		}
	})

	t.Run("invalid token falls back to a valid gh auth token", func(t *testing.T) {
		validateGitHubToken = func(s string) githubValidity {
			switch s {
			case "stale":
				return githubInvalid
			case "gho_good":
				return githubValid
			}
			return githubUnknown
		}
		discoverGhAuthToken = func() (string, error) { return "gho_good", nil }
		tok, warn := ReconcileGitHubToken("stale")
		if tok != "gho_good" {
			t.Fatalf("token = %q, want gho_good", tok)
		}
		if warn == "" {
			t.Fatal("expected a fallback warning")
		}
	})

	t.Run("invalid token with no gh fallback keeps original and warns", func(t *testing.T) {
		validateGitHubToken = func(string) githubValidity { return githubInvalid }
		discoverGhAuthToken = func() (string, error) { return "", nil }
		tok, warn := ReconcileGitHubToken("stale")
		if tok != "stale" {
			t.Fatalf("token = %q, want stale (unchanged)", tok)
		}
		if warn == "" {
			t.Fatal("expected a failure warning")
		}
	})

	t.Run("invalid token where the gh token is also invalid keeps original and warns", func(t *testing.T) {
		validateGitHubToken = func(string) githubValidity { return githubInvalid }
		discoverGhAuthToken = func() (string, error) { return "also_bad", nil }
		tok, warn := ReconcileGitHubToken("stale")
		if tok != "stale" {
			t.Fatalf("token = %q, want stale (unchanged)", tok)
		}
		if warn == "" {
			t.Fatal("expected a failure warning")
		}
	})
}
