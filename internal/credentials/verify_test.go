package credentials

import "testing"

func TestVerifyGitHubTriState(t *testing.T) {
	tests := []struct {
		name string
		stub func(string) Validity
		want Validity
	}{
		{"accepted", func(string) Validity { return ValidityValid }, ValidityValid},
		{"rejected", func(string) Validity { return ValidityRejected }, ValidityRejected},
		{"offline stays unknown", func(string) Validity { return ValidityUnknown }, ValidityUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHost()
			h.VerifyGitHub = tt.stub
			if got := h.Verify(Credential{Key: KeyGHToken, Value: "t"}); got != tt.want {
				t.Fatalf("Verify() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVerifyAnthropicIsNotNetworkChecked(t *testing.T) {
	// Boot must not make an Anthropic API call. Expiry is the only signal on
	// that side, and it is already carried on the Credential.
	h := newTestHost()
	h.VerifyGitHub = func(string) Validity {
		t.Fatal("must not call GitHub for an Anthropic key")
		return ValidityUnknown
	}
	if got := h.Verify(Credential{Key: KeyClaudeOAuthToken, Value: "sk-ant-oat01-x"}); got != ValidityUnknown {
		t.Fatalf("Verify() = %v, want ValidityUnknown for Anthropic", got)
	}
}

func TestVerifyEmptyValueIsRejected(t *testing.T) {
	h := newTestHost()
	h.VerifyGitHub = func(string) Validity { return ValidityValid }
	if got := h.Verify(Credential{Key: KeyGHToken, Value: ""}); got != ValidityRejected {
		t.Fatalf("Verify() = %v, want ValidityRejected for an empty value", got)
	}
}

func TestVerifyWithoutATransportIsUnknown(t *testing.T) {
	h := newTestHost()
	h.VerifyGitHub = nil
	if got := h.Verify(Credential{Key: KeyGHToken, Value: "t"}); got != ValidityUnknown {
		t.Fatalf("Verify() = %v, want ValidityUnknown when verification is unavailable", got)
	}
}
