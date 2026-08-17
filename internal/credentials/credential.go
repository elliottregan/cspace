// Package credentials owns cspace credential resolution, policy, and
// reporting. It is the single answer to "what credential will this sandbox
// get, and from where" — a question that was previously reconstructed
// independently in cmd_up, cmd_keychain, and probes, with drift between
// them that produced wrong answers reading as healthy ones.
//
// The package has four verbs:
//
//	Resolve — every candidate cspace could deliver, ranked
//	Bake    — apply policy to produce the final container env
//	Verify  — does this credential still work, against the provider
//	Inspect — what a running sandbox actually carries
//
// Resolve and Inspect answer different questions on purpose. Credentials
// are baked into a container's environment at create time and are immutable
// for its life, so host-side resolution describes what a *new* sandbox would
// get, never what a running one holds.
package credentials

import "time"

// Source identifies where a credential came from. Lower values outrank
// higher ones: Source is the precedence order, declared once, so ranking
// cannot drift from rendering.
type Source int

const (
	SourceEnvFlag         Source = iota // --env KEY=value
	SourceProjectKeychain               // cspace-<project>-<KEY>
	SourceGlobalKeychain                // cspace-<KEY>
	SourceHostShell                     // ambient os.Getenv
	SourceAutoDiscovered                // gh auth token / Claude Code-credentials
)

var sourceLabels = map[Source]string{
	SourceEnvFlag:         "--env",
	SourceProjectKeychain: "keychain:project",
	SourceGlobalKeychain:  "keychain",
	SourceHostShell:       "host shell",
	SourceAutoDiscovered:  "auto-discovered",
}

func (s Source) String() string {
	if l, ok := sourceLabels[s]; ok {
		return l
	}
	return "unknown"
}

// Credential is one candidate value for one key.
type Credential struct {
	Key       string
	Value     string
	Source    Source
	Detail    string    // e.g. "cspace-resume-redux-GH_TOKEN"
	ExpiresAt time.Time // zero = this credential type carries no expiry
}

// Resolution is the ranked candidate stack for one key. Candidates[0] is
// the winner; later entries are fallback rungs for Bake's 401 ladder.
//
// The stack — rather than a single winner — is the type because a rejected
// credential must be replaceable without re-running resolution.
type Resolution struct {
	Key        string
	Candidates []Credential
}

// Winner returns the highest-ranked candidate, if any.
func (r Resolution) Winner() (Credential, bool) {
	if len(r.Candidates) == 0 {
		return Credential{}, false
	}
	return r.Candidates[0], true
}

// Durability describes how long a credential can be relied on. It is
// derived, never stored, so every reporting surface agrees.
type Durability int

const (
	Unknown  Durability = iota // could not be read or typed
	Durable                    // carries no expiry mechanism at all
	Expiring                   // expiry known and inside the runway threshold
	Expired                    // expiry known and in the past
)

func (d Durability) String() string {
	switch d {
	case Durable:
		return "durable"
	case Expiring:
		return "expiring"
	case Expired:
		return "expired"
	default:
		return "unknown"
	}
}

// DurabilityOf classifies a credential by whether its *type* carries an
// expiry — not by how confident we feel about it. An sk-ant-api key and a
// GitHub PAT both carry none, so both are Durable; revocation risk is
// Verify's job, not the durability label's.
func DurabilityOf(c Credential, now time.Time, runway time.Duration) Durability {
	if c.ExpiresAt.IsZero() {
		return Durable
	}
	if !c.ExpiresAt.After(now) {
		return Expired
	}
	if c.ExpiresAt.Sub(now) <= runway {
		return Expiring
	}
	return Durable
}
