package credentials

import "testing"

func TestApplyGroupPolicyAnthropicIsExclusive(t *testing.T) {
	in := map[string]Credential{
		KeyAnthropicAPIKey:  {Key: KeyAnthropicAPIKey, Value: "sk-ant-api-xyz", Source: SourceGlobalKeychain},
		KeyClaudeOAuthToken: {Key: KeyClaudeOAuthToken, Value: "sk-ant-oat-abc", Source: SourceAutoDiscovered},
	}
	out := ApplyGroupPolicy(in)
	if _, ok := out[KeyAnthropicAPIKey]; !ok {
		t.Fatal("higher-ranked carrier should survive")
	}
	if _, ok := out[KeyClaudeOAuthToken]; ok {
		t.Fatal("exactly one Anthropic carrier may ship")
	}
}

func TestApplyGroupPolicyRoutesByTokenPrefix(t *testing.T) {
	// An oat token misfiled under ANTHROPIC_API_KEY must move to the OAuth
	// carrier. Claude rejects a wrong-carrier token as "Invalid API key" and
	// the interactive CLI shows a spurious "custom API key" prompt.
	in := map[string]Credential{
		KeyAnthropicAPIKey: {Key: KeyAnthropicAPIKey, Value: "sk-ant-oat01-abc", Source: SourceGlobalKeychain},
	}
	out := ApplyGroupPolicy(in)
	if _, ok := out[KeyAnthropicAPIKey]; ok {
		t.Fatal("oat token must not ship on ANTHROPIC_API_KEY")
	}
	got, ok := out[KeyClaudeOAuthToken]
	if !ok || got.Value != "sk-ant-oat01-abc" {
		t.Fatalf("oat token should be rerouted, got %+v", out)
	}
}

func TestApplyGroupPolicyRoutesAPIKeyOffTheOAuthCarrier(t *testing.T) {
	in := map[string]Credential{
		KeyClaudeOAuthToken: {Key: KeyClaudeOAuthToken, Value: "sk-ant-api03-xyz", Source: SourceLegacyUserFile},
	}
	out := ApplyGroupPolicy(in)
	if _, ok := out[KeyClaudeOAuthToken]; ok {
		t.Fatal("api key must not ship on CLAUDE_CODE_OAUTH_TOKEN")
	}
	if got := out[KeyAnthropicAPIKey]; got.Value != "sk-ant-api03-xyz" {
		t.Fatalf("api key should be rerouted, got %+v", out)
	}
}

func TestApplyGroupPolicyMirrorPicksBySourceRankNotNameOrder(t *testing.T) {
	// Regression guard for the propagateFamily clobber: the old
	// implementation picked the first non-empty by NAME order, so GH_TOKEN
	// won despite ranking below the --env value.
	in := map[string]Credential{
		KeyGHToken:     {Key: KeyGHToken, Value: "low-rank", Source: SourceAutoDiscovered},
		KeyGitHubToken: {Key: KeyGitHubToken, Value: "high-rank", Source: SourceEnvFlag},
	}
	out := ApplyGroupPolicy(in)
	for _, k := range []string{KeyGHToken, KeyGitHubToken, KeyGitHubPAT} {
		if out[k].Value != "high-rank" {
			t.Errorf("%s = %q, want the highest-ranked value mirrored to all three", k, out[k].Value)
		}
	}
}

func TestApplyGroupPolicyMirrorFillsAllThreeNames(t *testing.T) {
	// gh reads GH_TOKEN, the GitHub MCP server reads
	// GITHUB_PERSONAL_ACCESS_TOKEN, Actions-style tooling reads GITHUB_TOKEN.
	in := map[string]Credential{
		KeyGHToken: {Key: KeyGHToken, Value: "tok", Source: SourceGlobalKeychain},
	}
	out := ApplyGroupPolicy(in)
	for _, k := range []string{KeyGHToken, KeyGitHubToken, KeyGitHubPAT} {
		if out[k].Value != "tok" {
			t.Errorf("%s = %q, want the value mirrored", k, out[k].Value)
		}
		if out[k].Key != k {
			t.Errorf("%s carries Key=%q, want the credential relabelled to its slot", k, out[k].Key)
		}
	}
}

func TestApplyGroupPolicyLeavesAppVarsAlone(t *testing.T) {
	in := map[string]Credential{
		"RESEND_API_KEY": {Key: "RESEND_API_KEY", Value: "app", Source: SourceLegacyUserFile},
	}
	if got := ApplyGroupPolicy(in); got["RESEND_API_KEY"].Value != "app" {
		t.Fatalf("app vars must pass through, got %+v", got)
	}
}

func TestIsOwnedKey(t *testing.T) {
	for _, k := range Keys() {
		if !IsOwnedKey(k) {
			t.Errorf("IsOwnedKey(%q) = false", k)
		}
	}
	if IsOwnedKey("RESEND_API_KEY") {
		t.Error("app vars must not be claimed")
	}
}

func TestKeysMatchesTheDocumentedFive(t *testing.T) {
	want := []string{
		"ANTHROPIC_API_KEY",
		"CLAUDE_CODE_OAUTH_TOKEN",
		"GH_TOKEN",
		"GITHUB_TOKEN",
		"GITHUB_PERSONAL_ACCESS_TOKEN",
	}
	got := Keys()
	if len(got) != len(want) {
		t.Fatalf("Keys() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Keys()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
