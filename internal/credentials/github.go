package credentials

import (
	"net/http"
	"time"
)

// githubAPIUserURL requires authentication and returns 200 for any usable
// token, 401 for a revoked/expired/garbage one — the cheapest way to ask
// GitHub "is this token good?".
const githubAPIUserURL = "https://api.github.com/user"

// githubHTTPClient has a short timeout so a slow or blocked network can
// never stall `cspace up`. A timeout surfaces as ValidityUnknown, which is
// deliberately a no-op: cspace never downgrades a credential it cannot
// positively disprove.
var githubHTTPClient = &http.Client{Timeout: 5 * time.Second}

// liveVerifyGitHub asks GitHub whether a token is still accepted. It moved
// here from internal/secrets because it is credential policy, not a host
// primitive — and because keeping it beside the fallback ladder it feeds
// makes the ordering hazard visible: the old ReconcileGitHubToken bundled
// verification and replacement into one call that had to run before the
// env_file merge, so it checked a value the merge then discarded.
func liveVerifyGitHub(token string) Validity {
	if token == "" {
		return ValidityRejected
	}
	req, err := http.NewRequest(http.MethodGet, githubAPIUserURL, nil)
	if err != nil {
		return ValidityUnknown
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	// GitHub rejects requests without a User-Agent (403), so always send one.
	req.Header.Set("User-Agent", "cspace")

	resp, err := githubHTTPClient.Do(req)
	if err != nil {
		return ValidityUnknown
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return ValidityValid
	case http.StatusUnauthorized:
		return ValidityRejected
	default:
		// 403 (rate limit / secondary limit), 5xx, etc. don't prove the
		// token is bad — indeterminate rather than nuke a good credential.
		return ValidityUnknown
	}
}
