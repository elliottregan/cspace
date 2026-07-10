package secrets

import (
	"net/http"
	"time"
)

// githubAPIUserURL requires authentication and returns 200 for any usable
// token, 401 for a revoked/expired/garbage one — the cheapest way to ask
// GitHub "is this token good?".
const githubAPIUserURL = "https://api.github.com/user"

// githubValidity is a tri-state so callers can tell "known bad" (safe to
// replace) apart from "couldn't tell" (leave alone — e.g. offline, rate
// limited). cspace never downgrades a credential it can't positively disprove.
type githubValidity int

const (
	githubUnknown githubValidity = iota // network error / unexpected status
	githubValid                         // authenticated (HTTP 200)
	githubInvalid                       // definitively rejected (HTTP 401)
)

// validateGitHubToken is a package seam so tests avoid real network calls.
var validateGitHubToken = liveValidateGitHubToken

// githubHTTPClient has a short timeout so a slow/blocked network can never
// stall `cspace up` — a timeout surfaces as githubUnknown, which is a no-op.
var githubHTTPClient = &http.Client{Timeout: 5 * time.Second}

func liveValidateGitHubToken(token string) githubValidity {
	if token == "" {
		return githubInvalid
	}
	req, err := http.NewRequest(http.MethodGet, githubAPIUserURL, nil)
	if err != nil {
		return githubUnknown
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	// GitHub rejects requests without a User-Agent (403), so always send one.
	req.Header.Set("User-Agent", "cspace")

	resp, err := githubHTTPClient.Do(req)
	if err != nil {
		return githubUnknown
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return githubValid
	case http.StatusUnauthorized:
		return githubInvalid
	default:
		// 403 (rate limit / secondary limit), 5xx, etc. don't prove the token
		// is bad — treat as indeterminate rather than nuke a good credential.
		return githubUnknown
	}
}

// ReconcileGitHubToken validates the GitHub token cspace is about to inject
// into a sandbox and, when it is definitively invalid, substitutes a valid
// `gh auth token`. It returns the token to use (possibly unchanged) and a
// human-readable warning ("" when there is nothing to report).
//
// This closes the gap that let a stale GH_TOKEN / GITHUB_PERSONAL_ACCESS_TOKEN
// (e.g. leaked in from a project .env) shadow the valid `gh auth token` and
// silently break git/gh inside the sandbox.
//
// Behavior:
//   - empty input: no-op, no warning.
//   - valid, or indeterminate (offline / unexpected API status): the token is
//     returned unchanged with no warning.
//   - definitively invalid (HTTP 401): falls back to `gh auth token` when that
//     is itself valid; otherwise keeps the original token and warns that
//     GitHub operations in the sandbox will fail.
func ReconcileGitHubToken(current string) (token, warning string) {
	if current == "" {
		return "", ""
	}
	switch validateGitHubToken(current) {
	case githubValid, githubUnknown:
		return current, ""
	}

	// current is definitively invalid — try the gh CLI's own token.
	fallback, err := discoverGhAuthToken()
	if err == nil && fallback != "" && fallback != current && validateGitHubToken(fallback) == githubValid {
		return fallback, "resolved GitHub token was rejected by GitHub (401); fell back to `gh auth token`. " +
			"Fix or remove the stale GH_TOKEN / GITHUB_PERSONAL_ACCESS_TOKEN source (often a project .env) to silence this."
	}
	return current, "resolved GitHub token was rejected by GitHub (401) and no valid `gh auth token` fallback " +
		"is available — git and gh in the sandbox will fail until you fix the credential."
}
