package credentials

import (
	"strings"
	"testing"
	"time"
)

func TestSummaryLineNamesCarrierSourceAndDurability(t *testing.T) {
	now := time.Now()
	b := BakeResult{
		Winners: map[string]Credential{
			KeyClaudeOAuthToken: {Key: KeyClaudeOAuthToken, Source: SourceGlobalKeychain},
			KeyGHToken:          {Key: KeyGHToken, Source: SourceProjectKeychain, Detail: "cspace-resume-redux-GH_TOKEN"},
		},
		Validities: map[string]Validity{KeyGHToken: ValidityValid},
	}
	line, warn := SummaryLine(b, now, 4*time.Hour)
	if !strings.Contains(line, "claude") || !strings.Contains(line, "github") {
		t.Errorf("line = %q, want both groups named", line)
	}
	if !strings.Contains(line, "keychain") {
		t.Errorf("line = %q, want the source named", line)
	}
	if !strings.Contains(line, "durable") {
		t.Errorf("line = %q, want the durability named", line)
	}
	if warn != "" {
		t.Errorf("warning = %q, want none for a durable credential", warn)
	}
}

func TestSummaryLineNamesTheProjectScope(t *testing.T) {
	// The scope label is what makes a silently-detached project-scoped entry
	// visible: renaming a directory drops resolution to the broader global
	// credential, and this is where that downgrade shows up.
	b := BakeResult{Winners: map[string]Credential{
		KeyGHToken: {Key: KeyGHToken, Source: SourceProjectKeychain, Detail: "cspace-resume-redux-GH_TOKEN"},
	}}
	line, _ := SummaryLine(b, time.Now(), 4*time.Hour)
	if !strings.Contains(line, "keychain:resume-redux") {
		t.Fatalf("line = %q, want the project scope named", line)
	}
}

func TestSummaryLineEscalatesInsideRunway(t *testing.T) {
	now := time.Now()
	b := BakeResult{Winners: map[string]Credential{
		KeyClaudeOAuthToken: {Key: KeyClaudeOAuthToken, Source: SourceAutoDiscovered, ExpiresAt: now.Add(time.Hour)},
	}}
	_, warn := SummaryLine(b, now, 4*time.Hour)
	if warn == "" || !strings.Contains(warn, "keychain init") {
		t.Fatalf("warning = %q, want an escalation naming the durable fix", warn)
	}
}

func TestSummaryLineDoesNotEscalateBeyondRunway(t *testing.T) {
	now := time.Now()
	b := BakeResult{Winners: map[string]Credential{
		KeyClaudeOAuthToken: {Key: KeyClaudeOAuthToken, Source: SourceAutoDiscovered, ExpiresAt: now.Add(9 * time.Hour)},
	}}
	if _, warn := SummaryLine(b, now, 4*time.Hour); warn != "" {
		t.Fatalf("warning = %q, want none beyond the runway", warn)
	}
}

func TestSummaryLineZeroRunwayDisablesEscalation(t *testing.T) {
	now := time.Now()
	b := BakeResult{Winners: map[string]Credential{
		KeyClaudeOAuthToken: {Key: KeyClaudeOAuthToken, Source: SourceAutoDiscovered, ExpiresAt: now.Add(time.Minute)},
	}}
	if _, warn := SummaryLine(b, now, 0); warn != "" {
		t.Fatalf("warning = %q, want escalation disabled at runway 0", warn)
	}
}

func TestSummaryLineReportsRejectedCredential(t *testing.T) {
	b := BakeResult{
		Winners:    map[string]Credential{KeyGHToken: {Key: KeyGHToken, Source: SourceGlobalKeychain}},
		Validities: map[string]Validity{KeyGHToken: ValidityRejected},
	}
	line, warn := SummaryLine(b, time.Now(), 4*time.Hour)
	if !strings.Contains(line, "rejected") && !strings.Contains(warn, "rejected") {
		t.Fatalf("line=%q warn=%q, want the 401 surfaced", line, warn)
	}
}

func TestSummaryLineNotesShadowedKeysWithoutEnumerating(t *testing.T) {
	b := BakeResult{
		Winners:  map[string]Credential{KeyGHToken: {Key: KeyGHToken, Source: SourceGlobalKeychain}},
		Shadowed: []string{"CLAUDE_CODE_OAUTH_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"},
	}
	line, _ := SummaryLine(b, time.Now(), 4*time.Hour)
	if !strings.Contains(line, "project env") {
		t.Errorf("line = %q, want a compact note that a project file lost", line)
	}
	if strings.Contains(line, "GITHUB_TOKEN") {
		t.Errorf("line = %q, must not enumerate keys — doctor carries the detail", line)
	}
}

func TestSummaryLineReportsMissingCredentials(t *testing.T) {
	// The accepted breakage needs a visible surface: a project whose .env
	// was its only source now resolves nothing.
	b := BakeResult{Shadowed: []string{"GH_TOKEN"}}
	line, warn := SummaryLine(b, time.Now(), 4*time.Hour)
	if !strings.Contains(line, "none") && !strings.Contains(warn, "no ") {
		t.Fatalf("line=%q warn=%q, want the absence surfaced", line, warn)
	}
}

func TestSummaryLineSkipsAbsentGroups(t *testing.T) {
	b := BakeResult{Winners: map[string]Credential{
		KeyGHToken: {Key: KeyGHToken, Source: SourceGlobalKeychain},
	}}
	line, _ := SummaryLine(b, time.Now(), 4*time.Hour)
	if strings.Contains(line, "claude") {
		t.Fatalf("line = %q, want the absent Anthropic group omitted from the segments", line)
	}
}
