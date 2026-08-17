package credentials

import "strings"

// The five cspace-owned env var names, in two families with opposite
// policies. Declaring this once is the point: it was previously tribal
// knowledge spread across normalizeAnthropicCarrier, two carrier-dedup
// blocks in cmd_up, and two propagateFamily calls.
const (
	KeyAnthropicAPIKey  = "ANTHROPIC_API_KEY"
	KeyClaudeOAuthToken = "CLAUDE_CODE_OAUTH_TOKEN"
	KeyGHToken          = "GH_TOKEN"
	KeyGitHubToken      = "GITHUB_TOKEN"
	KeyGitHubPAT        = "GITHUB_PERSONAL_ACCESS_TOKEN"
)

var (
	anthropicGroup = []string{KeyAnthropicAPIKey, KeyClaudeOAuthToken}
	githubGroup    = []string{KeyGHToken, KeyGitHubToken, KeyGitHubPAT}
)

// Keys returns the five cspace-owned env var names.
func Keys() []string {
	out := make([]string, 0, len(anthropicGroup)+len(githubGroup))
	out = append(out, anthropicGroup...)
	out = append(out, githubGroup...)
	return out
}

// IsOwnedKey reports whether cspace owns this env var name. Callers use it
// to decide what to strip from a project's environment — everything else
// flows through untouched.
func IsOwnedKey(k string) bool {
	for _, owned := range Keys() {
		if owned == k {
			return true
		}
	}
	return false
}

// ApplyGroupPolicy enforces both family policies on a per-key winner map:
//
//	Anthropic — Exclusive: exactly one carrier ships, routed by the token's
//	            own prefix. Claude rejects a wrong-carrier token as
//	            "Invalid API key" and prompts for a custom API key.
//	GitHub    — Mirror: the highest-ranked value ships under all three
//	            names, because gh reads GH_TOKEN, the GitHub MCP server
//	            reads GITHUB_PERSONAL_ACCESS_TOKEN, and Actions-style
//	            tooling reads GITHUB_TOKEN.
//
// Keys outside both families pass through unchanged.
func ApplyGroupPolicy(in map[string]Credential) map[string]Credential {
	out := make(map[string]Credential, len(in))
	for k, v := range in {
		out[k] = v
	}
	applyAnthropicExclusive(out)
	applyGitHubMirror(out)
	return out
}

func applyAnthropicExclusive(env map[string]Credential) {
	best, ok := bestBySourceRank(env, anthropicGroup)
	for _, k := range anthropicGroup {
		delete(env, k)
	}
	if !ok {
		return
	}
	carrier := carrierFor(best.Value)
	best.Key = carrier
	env[carrier] = best
}

// carrierFor routes an Anthropic token to the env var Claude Code expects,
// by the token's own format rather than by whichever slot it was filed
// under. This makes resolution correct even when a value was misfiled —
// historically `cspace keychain init` stored everything under
// cspace-ANTHROPIC_API_KEY.
func carrierFor(value string) string {
	switch {
	case strings.HasPrefix(value, "sk-ant-oat"):
		return KeyClaudeOAuthToken
	case strings.HasPrefix(value, "sk-ant-api"):
		return KeyAnthropicAPIKey
	default:
		// Unrecognized format: keep it on the OAuth carrier, which is what
		// `claude setup-token` emits and what auto-discovery fills.
		return KeyClaudeOAuthToken
	}
}

func applyGitHubMirror(env map[string]Credential) {
	best, ok := bestBySourceRank(env, githubGroup)
	if !ok {
		for _, k := range githubGroup {
			delete(env, k)
		}
		return
	}
	for _, k := range githubGroup {
		c := best
		c.Key = k
		env[k] = c
	}
}

// bestBySourceRank picks the highest-ranked candidate across a group.
// Rank, never name order — name order is exactly what made propagateFamily
// overwrite a project's distinct GITHUB_PERSONAL_ACCESS_TOKEN with its
// GH_TOKEN.
func bestBySourceRank(env map[string]Credential, group []string) (Credential, bool) {
	var best Credential
	found := false
	for _, k := range group {
		c, ok := env[k]
		if !ok || c.Value == "" {
			continue
		}
		if !found || c.Source < best.Source {
			best, found = c, true
		}
	}
	return best, found
}
