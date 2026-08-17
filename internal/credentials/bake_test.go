package credentials

import "testing"

func TestBakeStripsProjectEnvFileCredentials(t *testing.T) {
	// The regression guard for the resume-redux failure: a dead PAT in the
	// project's .env must not reach the container, and must be recorded.
	h := newTestHost()
	h.VerifyGitHub = func(string) Validity { return ValidityValid }

	res := map[string]Resolution{
		KeyGHToken: {Key: KeyGHToken, Candidates: []Credential{
			{Key: KeyGHToken, Value: "cspace-token", Source: SourceGlobalKeychain},
		}},
	}
	appEnv := map[string]string{
		"GH_TOKEN":       "dead-pat-from-dot-env",
		"RESEND_API_KEY": "app-var",
	}
	got := h.Bake(res, appEnv)

	if got.Env[KeyGHToken] != "cspace-token" {
		t.Errorf("GH_TOKEN = %q, want cspace's value to win", got.Env[KeyGHToken])
	}
	if got.Env["RESEND_API_KEY"] != "app-var" {
		t.Error("app vars must pass through untouched")
	}
	if len(got.Shadowed) != 1 || got.Shadowed[0] != KeyGHToken {
		t.Errorf("Shadowed = %v, want [GH_TOKEN]", got.Shadowed)
	}
}

func TestBakeVerifiesTheValueThatShipsNotTheOneResolved(t *testing.T) {
	// The env_file value must never be what gets verified, and the baked
	// value must always be.
	var checked []string
	h := newTestHost()
	h.VerifyGitHub = func(tok string) Validity {
		checked = append(checked, tok)
		return ValidityValid
	}
	res := map[string]Resolution{
		KeyGHToken: {Key: KeyGHToken, Candidates: []Credential{
			{Key: KeyGHToken, Value: "cspace-token", Source: SourceGlobalKeychain},
		}},
	}
	h.Bake(res, map[string]string{KeyGHToken: "dead-pat"})

	for _, c := range checked {
		if c == "dead-pat" {
			t.Fatal("verified the env_file value; must verify what ships")
		}
	}
	if len(checked) == 0 || checked[0] != "cspace-token" {
		t.Fatalf("checked = %v, want the baked value verified", checked)
	}
}

func TestBakeIgnoresEnvFileEvenWhenCspaceResolvesNothing(t *testing.T) {
	// Decision 3 is unconditional. This is the accepted breakage: a project
	// whose .env was the only source now boots with no credential.
	h := newTestHost()
	got := h.Bake(map[string]Resolution{}, map[string]string{KeyGHToken: "only-source"})
	if v, present := got.Env[KeyGHToken]; present {
		t.Fatalf("GH_TOKEN = %q, want it absent", v)
	}
	if len(got.Shadowed) != 1 || got.Shadowed[0] != KeyGHToken {
		t.Errorf("Shadowed = %v, want the ignored key recorded for reporting", got.Shadowed)
	}
}

func TestBakeFallbackLadderAdvancesPast401(t *testing.T) {
	h := newTestHost()
	h.VerifyGitHub = func(tok string) Validity {
		if tok == "rejected" {
			return ValidityRejected
		}
		return ValidityValid
	}
	res := map[string]Resolution{
		KeyGHToken: {Key: KeyGHToken, Candidates: []Credential{
			{Key: KeyGHToken, Value: "rejected", Source: SourceGlobalKeychain},
			{Key: KeyGHToken, Value: "good", Source: SourceAutoDiscovered},
		}},
	}
	got := h.Bake(res, nil)
	if got.Env[KeyGHToken] != "good" {
		t.Fatalf("GH_TOKEN = %q, want the ladder to advance to the valid rung", got.Env[KeyGHToken])
	}
	if got.Winners[KeyGHToken].Source != SourceAutoDiscovered {
		t.Errorf("winner source = %v, want the rung that actually won", got.Winners[KeyGHToken].Source)
	}
}

func TestBakeNetworkErrorDoesNotAdvanceLadder(t *testing.T) {
	h := newTestHost()
	h.VerifyGitHub = func(string) Validity { return ValidityUnknown }
	res := map[string]Resolution{
		KeyGHToken: {Key: KeyGHToken, Candidates: []Credential{
			{Key: KeyGHToken, Value: "first", Source: SourceGlobalKeychain},
			{Key: KeyGHToken, Value: "second", Source: SourceAutoDiscovered},
		}},
	}
	if got := h.Bake(res, nil); got.Env[KeyGHToken] != "first" {
		t.Fatalf("GH_TOKEN = %q, want the offline case to keep the top rung", got.Env[KeyGHToken])
	}
}

func TestBakeAllRungsRejectedStillShipsTopRung(t *testing.T) {
	h := newTestHost()
	h.VerifyGitHub = func(string) Validity { return ValidityRejected }
	res := map[string]Resolution{
		KeyGHToken: {Key: KeyGHToken, Candidates: []Credential{
			{Key: KeyGHToken, Value: "bad1", Source: SourceGlobalKeychain},
			{Key: KeyGHToken, Value: "bad2", Source: SourceAutoDiscovered},
		}},
	}
	got := h.Bake(res, nil)
	if got.Env[KeyGHToken] != "bad1" {
		t.Errorf("GH_TOKEN = %q, want the top rung shipped anyway", got.Env[KeyGHToken])
	}
	if got.Validities[KeyGHToken] != ValidityRejected {
		t.Errorf("validity = %v, want the rejection reported", got.Validities[KeyGHToken])
	}
}

func TestBakeAppliesGroupPolicy(t *testing.T) {
	h := newTestHost()
	h.VerifyGitHub = func(string) Validity { return ValidityValid }
	res := map[string]Resolution{
		KeyGHToken: {Key: KeyGHToken, Candidates: []Credential{
			{Key: KeyGHToken, Value: "tok", Source: SourceGlobalKeychain},
		}},
	}
	got := h.Bake(res, nil)
	for _, k := range []string{KeyGHToken, KeyGitHubToken, KeyGitHubPAT} {
		if got.Env[k] != "tok" {
			t.Errorf("%s = %q, want the value mirrored to all three names", k, got.Env[k])
		}
	}
}

func TestBakeShipsExactlyOneAnthropicCarrier(t *testing.T) {
	h := newTestHost()
	res := map[string]Resolution{
		KeyAnthropicAPIKey: {Key: KeyAnthropicAPIKey, Candidates: []Credential{
			{Key: KeyAnthropicAPIKey, Value: "sk-ant-oat01-x", Source: SourceGlobalKeychain},
		}},
	}
	got := h.Bake(res, nil)
	if _, ok := got.Env[KeyAnthropicAPIKey]; ok {
		t.Error("an oat token must not ship on ANTHROPIC_API_KEY")
	}
	if got.Env[KeyClaudeOAuthToken] != "sk-ant-oat01-x" {
		t.Errorf("CLAUDE_CODE_OAUTH_TOKEN = %q, want the rerouted token", got.Env[KeyClaudeOAuthToken])
	}
}

func TestBakeLeavesAppEnvUntouchedWhenNoCredentialsResolve(t *testing.T) {
	h := newTestHost()
	appEnv := map[string]string{"NODE_ENV": "development", "CONVEX_URL": "http://x"}
	got := h.Bake(map[string]Resolution{}, appEnv)
	for k, v := range appEnv {
		if got.Env[k] != v {
			t.Errorf("%s = %q, want %q", k, got.Env[k], v)
		}
	}
	if len(got.Shadowed) != 0 {
		t.Errorf("Shadowed = %v, want empty", got.Shadowed)
	}
}
