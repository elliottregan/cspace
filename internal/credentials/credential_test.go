package credentials

import (
	"testing"
	"time"
)

func TestDurabilityOf(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	runway := 4 * time.Hour

	tests := []struct {
		name string
		cred Credential
		want Durability
	}{
		{"no expiry carried is durable", Credential{Key: "ANTHROPIC_API_KEY"}, Durable},
		{"expiry in the past", Credential{ExpiresAt: now.Add(-time.Minute)}, Expired},
		{"expiry inside runway", Credential{ExpiresAt: now.Add(time.Hour)}, Expiring},
		{"expiry beyond runway", Credential{ExpiresAt: now.Add(9 * time.Hour)}, Durable},
		{"expiry exactly at runway edge is expiring", Credential{ExpiresAt: now.Add(runway)}, Expiring},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DurabilityOf(tt.cred, now, runway); got != tt.want {
				t.Fatalf("DurabilityOf() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolutionWinner(t *testing.T) {
	empty := Resolution{Key: "GH_TOKEN"}
	if _, ok := empty.Winner(); ok {
		t.Fatal("empty Resolution should have no winner")
	}
	r := Resolution{Key: "GH_TOKEN", Candidates: []Credential{
		{Key: "GH_TOKEN", Value: "a", Source: SourceGlobalKeychain},
		{Key: "GH_TOKEN", Value: "b", Source: SourceAutoDiscovered},
	}}
	w, ok := r.Winner()
	if !ok || w.Value != "a" {
		t.Fatalf("Winner() = %+v, %v; want first candidate", w, ok)
	}
}

func TestSourceStringIsStableForRendering(t *testing.T) {
	want := map[Source]string{
		SourceEnvFlag:         "--env",
		SourceProjectKeychain: "keychain:project",
		SourceGlobalKeychain:  "keychain",
		SourceHostShell:       "host shell",
		SourceAutoDiscovered:  "auto-discovered",
	}
	for src, label := range want {
		if got := src.String(); got != label {
			t.Errorf("Source(%d).String() = %q, want %q", src, got, label)
		}
	}
}

// TestSourceOrderIsPrecedence pins the enum order, because Source doubles as
// the precedence ranking — a reordering here silently reranks resolution.
func TestSourceOrderIsPrecedence(t *testing.T) {
	ordered := []Source{
		SourceEnvFlag,
		SourceProjectKeychain,
		SourceGlobalKeychain,
		SourceHostShell,
		SourceAutoDiscovered,
	}
	for i := 1; i < len(ordered); i++ {
		if ordered[i-1] >= ordered[i] {
			t.Fatalf("Source %v must outrank %v", ordered[i-1], ordered[i])
		}
	}
}
