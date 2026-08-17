package credentials

import (
	"os"
	"testing"
)

func TestParseBakedEnvFromRealInspectFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/inspect-1.2.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	env, err := ParseBakedEnv(raw)
	if err != nil {
		t.Fatalf("ParseBakedEnv() error = %v", err)
	}
	if env[KeyGHToken] == "" {
		t.Fatalf("want GH_TOKEN in the baked env, got %d keys", len(env))
	}
	// A plain env var proves we are reading the whole environment, not just
	// keys that happen to look like credentials.
	if env["CSPACE_PROJECT"] != "resume-redux" {
		t.Errorf("CSPACE_PROJECT = %q, want the fixture's project", env["CSPACE_PROJECT"])
	}
}

func TestParseBakedEnvAcceptsABareObject(t *testing.T) {
	// 1.1.x pretty-prints and some paths emit a bare object rather than a
	// single-element array; adapter.go:4-11 documents that this CLI reshapes
	// its JSON across versions.
	raw := []byte(`{"configuration":{"initProcess":{"environment":["GH_TOKEN=x"]}}}`)
	env, err := ParseBakedEnv(raw)
	if err != nil {
		t.Fatalf("ParseBakedEnv() error = %v", err)
	}
	if env[KeyGHToken] != "x" {
		t.Fatalf("GH_TOKEN = %q, want x", env[KeyGHToken])
	}
}

func TestParseBakedEnvKeepsValuesContainingEquals(t *testing.T) {
	raw := []byte(`[{"configuration":{"initProcess":{"environment":["A=b=c"]}}}]`)
	env, err := ParseBakedEnv(raw)
	if err != nil {
		t.Fatalf("ParseBakedEnv() error = %v", err)
	}
	if env["A"] != "b=c" {
		t.Fatalf("A = %q, want b=c", env["A"])
	}
}

func TestParseBakedEnvErrorsOnGarbage(t *testing.T) {
	if _, err := ParseBakedEnv([]byte("not json")); err == nil {
		t.Fatal("want an error for unparseable inspect output")
	}
}

func TestParseBakedEnvErrorsOnEmptyRecordSet(t *testing.T) {
	if _, err := ParseBakedEnv([]byte(`[]`)); err == nil {
		t.Fatal("want an error when inspect returns no records")
	}
}

func TestDivergedComparesBakedAgainstResolved(t *testing.T) {
	baked := map[string]string{KeyGHToken: "old", KeyAnthropicAPIKey: "same"}
	winners := map[string]Credential{
		KeyGHToken:         {Key: KeyGHToken, Value: "new"},
		KeyAnthropicAPIKey: {Key: KeyAnthropicAPIKey, Value: "same"},
	}
	got := Diverged(baked, winners)
	if len(got) != 1 || got[0] != KeyGHToken {
		t.Fatalf("Diverged() = %v, want [GH_TOKEN]", got)
	}
}

func TestDivergedFlagsAKeyMissingFromTheContainer(t *testing.T) {
	winners := map[string]Credential{KeyGHToken: {Key: KeyGHToken, Value: "new"}}
	if got := Diverged(map[string]string{}, winners); len(got) != 1 {
		t.Fatalf("Diverged() = %v, want the absent key flagged", got)
	}
}

func TestDivergedIgnoresKeysCspaceNoLongerResolves(t *testing.T) {
	// A container holding a credential cspace no longer resolves is a
	// missing-credential story, not a divergence story. Reporting both would
	// double-count it.
	baked := map[string]string{KeyGHToken: "old"}
	if got := Diverged(baked, map[string]Credential{}); len(got) != 0 {
		t.Fatalf("Diverged() = %v, want empty", got)
	}
}

func TestDivergedIgnoresNonCspaceKeys(t *testing.T) {
	baked := map[string]string{"RESEND_API_KEY": "old"}
	winners := map[string]Credential{"RESEND_API_KEY": {Key: "RESEND_API_KEY", Value: "new"}}
	if got := Diverged(baked, winners); len(got) != 0 {
		t.Fatalf("Diverged() = %v, want app vars ignored", got)
	}
}
