package credentials

// Validity is tri-state so callers can tell "known bad" (safe to replace)
// apart from "couldn't tell" (leave alone — offline, rate limited).
// cspace never downgrades a credential it cannot positively disprove.
type Validity int

const (
	ValidityUnknown  Validity = iota // network error / unexpected status / not checkable
	ValidityValid                    // provider accepted it
	ValidityRejected                 // provider definitively rejected it
)

func (v Validity) String() string {
	switch v {
	case ValidityValid:
		return "verified"
	case ValidityRejected:
		return "rejected"
	default:
		return "unverified"
	}
}

// Verify is a pure predicate: it answers only "is this credential
// accepted", never "what should replace it". Replacement is Bake's ladder.
//
// The previous ReconcileGitHubToken coupled the two, which is part of why
// verification ended up running against a value the later env_file merge
// discarded — the check and the fallback had to share a call site, and that
// call site had to run before the merge.
//
// Only GitHub is network-checked. An Anthropic call would spend a token on
// every boot, and the expiry already rides on the Credential.
func (h Host) Verify(c Credential) Validity {
	switch c.Key {
	case KeyGHToken, KeyGitHubToken, KeyGitHubPAT:
		if c.Value == "" {
			return ValidityRejected
		}
		if h.VerifyGitHub == nil {
			return ValidityUnknown
		}
		return h.VerifyGitHub(c.Value)
	default:
		return ValidityUnknown
	}
}
