# Credential Resolution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract credential resolution, policy, and reporting into a single owning package so `cspace up`, `cspace doctor`, and `cspace keychain status` cannot disagree, and so a project's `env_file` can no longer shadow a cspace-owned credential unvalidated.

**Architecture:** A new `internal/credentials` package owns four verbs — `Resolve` (ranked candidate stack per key), `Bake` (policy → final container env), `Verify` (pure liveness predicate), `Inspect` (a running container's baked env). `internal/secrets` is demoted to primitives (dotenv parsing, Keychain I/O, host discovery) and stops making policy decisions. `internal/cli` becomes a pure consumer.

**Tech Stack:** Go 1.x (see `go.mod`), Cobra CLI, `go test` table-driven tests, no new dependencies.

## Global Constraints

- **Spec:** `docs/superpowers/specs/2026-08-17-credential-resolution-design.md` is the contract. Read it before Task 1.
- **The five cspace-owned keys, in this exact order** (`secrets.go:30-36`): `ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN`, `GITHUB_PERSONAL_ACCESS_TOKEN`.
- **Precedence, rank 1 highest:** `--env` → project Keychain (`cspace-<project>-<KEY>`) → global Keychain (`cspace-<KEY>`) → legacy `secrets.env` (project file, then user file) → ambient host shell → auto-discovery. Compose `env_file` and devcontainer `containerEnv` are **ignored** for these five keys, unconditionally.
- **Never touch the real Keychain, real `gh`, or the network in tests.** Use the existing package-seam pattern: `discoverClaudeOauthToken`/`discoverGhAuthToken` (`secrets.go:50-53`), `timeNow` (`secrets.go:57`), `validateGitHubToken`/`githubHTTPClient` (`github.go:25,29`).
- **Build via `make`**, never bare `go build` — `internal/assets/embedded/` is gitignored and populated by `make sync-embedded`.
- **`make check` must pass** before any task is considered done. It runs fmt-check + vet + lint + test. `golangci-lint` may not be installed locally; if `make lint` errors with "No such file or directory", run `make fmt-check vet test` and note that lint is CI-only.
- **Commit after every task**, using the repo's style: short imperative sentence describing what changed and why. End every commit message with:
  `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`
- **Non-darwin:** `keychain_other.go:10-12` makes Keychain a no-op (`WriteKeychain` silently returns nil) and Anthropic auto-discovery absent. `secrets.env` is the canonical durable path off macOS and must never be reported as deprecated there.

### Deviation from the spec, applied throughout

The spec's Output Surfaces section calls for a new `PhaseCredentials` enum value and a persistent note channel on `overlay.Reporter`. **This plan does not do that.** `overlay.Phase` drives progress arithmetic (`TotalPhases = int(PhaseReady)`, `focusSaturationPhase = int(PhaseSupervisor)`, `overlay.go:44,52`), so inserting a phase renumbers all eight.

It is unnecessary: today's warning lands after `overlay.Start` only because it depends on the compose `env_file` merge at `cmd_up.go:349`. Once cspace's resolution is independent of `env_file` (Decision 3), all credential work can run **before** `overlay.Start` at `cmd_up.go:222` and print to the real terminal — the pattern `cmd_up.go:107-112` already documents and uses on purpose. Same goal (never shredded by bubbletea), no overlay changes, no renumbering. Task 8 is therefore a no-op placeholder retained only so task numbers match the spec's review; the summary line ships in Task 10.

---

### Task 1: Package foundation — Source, Credential, Resolution, Durability

**Files:**
- Create: `internal/credentials/credential.go`
- Test: `internal/credentials/credential_test.go`

**Interfaces:**
- Consumes: nothing
- Produces: `Source` (+ `String()`), `Credential`, `Resolution`, `Durability` (+ `String()`), `Resolution.Winner() (Credential, bool)`, `Durability` derivation `func DurabilityOf(c Credential, now time.Time, runway time.Duration) Durability`

- [ ] **Step 1: Write the failing test**

```go
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
		SourceEnvFlag:           "--env",
		SourceProjectKeychain:   "keychain:project",
		SourceGlobalKeychain:    "keychain",
		SourceLegacyProjectFile: "project secrets.env",
		SourceLegacyUserFile:    "user secrets.env",
		SourceHostShell:         "host shell",
		SourceAutoDiscovered:    "auto-discovered",
	}
	for src, label := range want {
		if got := src.String(); got != label {
			t.Errorf("Source(%d).String() = %q, want %q", src, got, label)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/credentials/ -run 'TestDurabilityOf|TestResolutionWinner|TestSourceString' -v`
Expected: FAIL — the package doesn't exist ("no Go files in ...").

- [ ] **Step 3: Write minimal implementation**

```go
// Package credentials owns cspace credential resolution, policy, and
// reporting. It is the single answer to "what credential will this sandbox
// get, and from where" — a question that was previously reconstructed
// independently in cmd_up, cmd_keychain, and probes, with drift between them.
package credentials

import "time"

// Source identifies where a credential came from. Lower values outrank
// higher ones: Source is the precedence order, declared once.
type Source int

const (
	SourceEnvFlag           Source = iota // --env KEY=value
	SourceProjectKeychain                 // cspace-<project>-<KEY>
	SourceGlobalKeychain                  // cspace-<KEY>
	SourceLegacyProjectFile               // <project>/.cspace/secrets.env
	SourceLegacyUserFile                  // ~/.cspace/secrets.env
	SourceHostShell                       // ambient os.Getenv
	SourceAutoDiscovered                  // gh auth token / Claude Code-credentials
)

var sourceLabels = map[Source]string{
	SourceEnvFlag:           "--env",
	SourceProjectKeychain:   "keychain:project",
	SourceGlobalKeychain:    "keychain",
	SourceLegacyProjectFile: "project secrets.env",
	SourceLegacyUserFile:    "user secrets.env",
	SourceHostShell:         "host shell",
	SourceAutoDiscovered:    "auto-discovered",
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
// the winner; later entries are fallback rungs for the 401 ladder in Bake.
// The stack — not a single winner — is the type because a rejected
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
// expiry — not by how confident we feel. An sk-ant-api key and a GitHub PAT
// both carry none, so both are Durable; revocation risk is Verify's job,
// not the durability label's.
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/credentials/ -v`
Expected: PASS, all subtests.

- [ ] **Step 5: Commit**

```bash
git add internal/credentials/credential.go internal/credentials/credential_test.go
git commit -m "Add credentials package foundation: Source, Credential, Resolution, Durability

Source is the precedence order declared once, as an ordered enum, so
ranking cannot drift from rendering. Resolution carries the full ranked
candidate stack rather than a single winner, because the 401 fallback
ladder needs the losing candidates.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Key groups — Exclusive routing and Mirror by source rank

**Files:**
- Create: `internal/credentials/groups.go`
- Test: `internal/credentials/groups_test.go`

**Interfaces:**
- Consumes: `Credential`, `Source` (Task 1)
- Produces: `Keys() []string`, `IsOwnedKey(string) bool`, `ApplyGroupPolicy(map[string]Credential) map[string]Credential`

Two groups with opposite policies. This replaces `normalizeAnthropicCarrier` (`secrets.go:150-163`), both carrier-dedup blocks (`cmd_up.go:126-143`, `496-513`), and both `propagateFamily` calls (`cmd_up.go:520,531`).

- [ ] **Step 1: Write the failing test**

```go
package credentials

import "testing"

func TestApplyGroupPolicyAnthropicIsExclusive(t *testing.T) {
	in := map[string]Credential{
		"ANTHROPIC_API_KEY":       {Key: "ANTHROPIC_API_KEY", Value: "sk-ant-api-xyz", Source: SourceGlobalKeychain},
		"CLAUDE_CODE_OAUTH_TOKEN": {Key: "CLAUDE_CODE_OAUTH_TOKEN", Value: "sk-ant-oat-abc", Source: SourceAutoDiscovered},
	}
	out := ApplyGroupPolicy(in)
	if _, ok := out["ANTHROPIC_API_KEY"]; !ok {
		t.Fatal("higher-ranked carrier should survive")
	}
	if _, ok := out["CLAUDE_CODE_OAUTH_TOKEN"]; ok {
		t.Fatal("exactly one Anthropic carrier may ship")
	}
}

func TestApplyGroupPolicyRoutesByTokenPrefix(t *testing.T) {
	// An oat token misfiled under ANTHROPIC_API_KEY must move to the
	// OAuth carrier. Claude rejects a wrong-carrier token as "Invalid API key".
	in := map[string]Credential{
		"ANTHROPIC_API_KEY": {Key: "ANTHROPIC_API_KEY", Value: "sk-ant-oat01-abc", Source: SourceGlobalKeychain},
	}
	out := ApplyGroupPolicy(in)
	if _, ok := out["ANTHROPIC_API_KEY"]; ok {
		t.Fatal("oat token must not ship on ANTHROPIC_API_KEY")
	}
	got, ok := out["CLAUDE_CODE_OAUTH_TOKEN"]
	if !ok || got.Value != "sk-ant-oat01-abc" {
		t.Fatalf("oat token should be rerouted, got %+v", out)
	}
}

func TestApplyGroupPolicyMirrorPicksBySourceRankNotNameOrder(t *testing.T) {
	// This is the regression guard for the propagateFamily clobber:
	// today's implementation picks the first non-empty by NAME order, so
	// GH_TOKEN would win despite ranking lower than the --env value.
	in := map[string]Credential{
		"GH_TOKEN":     {Key: "GH_TOKEN", Value: "low-rank", Source: SourceAutoDiscovered},
		"GITHUB_TOKEN": {Key: "GITHUB_TOKEN", Value: "high-rank", Source: SourceEnvFlag},
	}
	out := ApplyGroupPolicy(in)
	for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GITHUB_PERSONAL_ACCESS_TOKEN"} {
		if out[k].Value != "high-rank" {
			t.Errorf("%s = %q, want the highest-ranked value mirrored to all three", k, out[k].Value)
		}
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/credentials/ -run TestApplyGroupPolicy -v`
Expected: FAIL — `undefined: ApplyGroupPolicy`.

- [ ] **Step 3: Write minimal implementation**

```go
package credentials

import "strings"

// The two credential families and their opposite policies. Declaring this
// once is the point: it was previously tribal knowledge spread across
// normalizeAnthropicCarrier, two carrier-dedup blocks, and propagateFamily.
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

// IsOwnedKey reports whether cspace owns this env var name.
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
//	Anthropic — Exclusive: exactly one carrier ships, routed by token prefix.
//	            Claude rejects a wrong-carrier token as "Invalid API key".
//	GitHub    — Mirror: the highest-ranked value ships under all three names,
//	            because gh reads GH_TOKEN, the MCP server reads
//	            GITHUB_PERSONAL_ACCESS_TOKEN, and Actions-style tooling reads
//	            GITHUB_TOKEN.
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
// by the token's own format rather than by which slot it was filed under.
func carrierFor(value string) string {
	if strings.HasPrefix(value, "sk-ant-oat") {
		return KeyClaudeOAuthToken
	}
	if strings.HasPrefix(value, "sk-ant-api") {
		return KeyAnthropicAPIKey
	}
	// Unrecognized format: keep it on the OAuth carrier, which is what
	// `claude setup-token` output uses and what auto-discovery fills.
	return KeyClaudeOAuthToken
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
// Rank, never name order — name order is the propagateFamily clobber.
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/credentials/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/credentials/groups.go internal/credentials/groups_test.go
git commit -m "Declare Anthropic Exclusive and GitHub Mirror group policies

Mirror selects by source rank, never by name order. propagateFamily's
name-order pick is what silently overwrote a project's distinct
GITHUB_PERSONAL_ACCESS_TOKEN with its GH_TOKEN.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Resolve — ranked candidate stack from all six sources

**Files:**
- Create: `internal/credentials/resolve.go`
- Test: `internal/credentials/resolve_test.go`
- Reference (do not modify yet): `internal/secrets/secrets.go`, `internal/secrets/keychain_darwin.go`

**Interfaces:**
- Consumes: `Credential`, `Source`, `Keys()` (Tasks 1-2)
- Produces:
  - `type Host struct` with swappable func fields — the test seam
  - `func (h Host) Resolve(project, projectRoot, userHome string, envFlags map[string]string) map[string]Resolution`

`Resolve` collects **every** candidate, ranked — it does not pick. Picking is `Winner()`; replacing a rejected pick is Bake's ladder.

- [ ] **Step 1: Write the failing test**

```go
package credentials

import (
	"testing"
	"time"
)

// newTestHost returns a Host with every external dependency stubbed out.
// No test touches the real Keychain, real gh, or the network.
func newTestHost() *Host {
	return &Host{
		ReadKeychain:    func(service string) (string, error) { return "", nil },
		ReadSecretsFile: func(path string) (map[string]string, error) { return nil, nil },
		LookupEnv:       func(string) (string, bool) { return "", false },
		DiscoverClaude:  func() (string, time.Time, error) { return "", time.Time{}, nil },
		DiscoverGh:      func() (string, error) { return "", nil },
	}
}

func TestResolveRanksProjectKeychainAboveGlobal(t *testing.T) {
	h := newTestHost()
	h.ReadKeychain = func(service string) (string, error) {
		switch service {
		case "cspace-resume-redux-GH_TOKEN":
			return "scoped", nil
		case "cspace-GH_TOKEN":
			return "global", nil
		}
		return "", nil
	}
	got := h.Resolve("resume-redux", "/p", "/home/u", nil)
	cands := got["GH_TOKEN"].Candidates
	if len(cands) != 2 {
		t.Fatalf("want 2 candidates, got %d: %+v", len(cands), cands)
	}
	if cands[0].Value != "scoped" || cands[0].Source != SourceProjectKeychain {
		t.Errorf("winner = %+v, want the project-scoped entry", cands[0])
	}
	if cands[1].Value != "global" || cands[1].Source != SourceGlobalKeychain {
		t.Errorf("fallback = %+v, want the global entry", cands[1])
	}
}

func TestResolveEnvFlagOutranksEverything(t *testing.T) {
	h := newTestHost()
	h.ReadKeychain = func(string) (string, error) { return "keychain", nil }
	h.LookupEnv = func(k string) (string, bool) { return "ambient", true }
	h.DiscoverGh = func() (string, error) { return "discovered", nil }

	got := h.Resolve("proj", "/p", "/home/u", map[string]string{"GH_TOKEN": "flag"})
	w, ok := got["GH_TOKEN"].Winner()
	if !ok || w.Source != SourceEnvFlag || w.Value != "flag" {
		t.Fatalf("winner = %+v, want the --env value", w)
	}
}

func TestResolveAmbientShellRanksBelowKeychain(t *testing.T) {
	// Behavior change 2: today os.Getenv("GH_TOKEN") wins outright at
	// cmd_up.go:523, letting a stale export shadow a scoped token.
	h := newTestHost()
	h.ReadKeychain = func(service string) (string, error) {
		if service == "cspace-GH_TOKEN" {
			return "keychain", nil
		}
		return "", nil
	}
	h.LookupEnv = func(string) (string, bool) { return "ambient", true }

	w, _ := h.Resolve("proj", "/p", "/home/u", nil)["GH_TOKEN"].Winner()
	if w.Source != SourceGlobalKeychain {
		t.Fatalf("winner source = %v, want Keychain to outrank ambient shell", w.Source)
	}
}

func TestResolveCarriesOAuthExpiry(t *testing.T) {
	exp := time.Date(2026, 8, 17, 15, 57, 0, 0, time.UTC)
	h := newTestHost()
	h.DiscoverClaude = func() (string, time.Time, error) { return "sk-ant-oat01-x", exp, nil }

	w, ok := h.Resolve("proj", "/p", "/home/u", nil)[KeyClaudeOAuthToken].Winner()
	if !ok || !w.ExpiresAt.Equal(exp) {
		t.Fatalf("winner = %+v, want the discovered expiry carried through", w)
	}
}

func TestResolveSkipsEmptyValues(t *testing.T) {
	h := newTestHost() // everything returns ""
	got := h.Resolve("proj", "/p", "/home/u", nil)
	for _, k := range Keys() {
		if len(got[k].Candidates) != 0 {
			t.Errorf("%s: want no candidates, got %+v", k, got[k].Candidates)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/credentials/ -run TestResolve -v`
Expected: FAIL — `undefined: Host`.

- [ ] **Step 3: Write minimal implementation**

```go
package credentials

import (
	"path/filepath"
	"time"
)

// Host holds the external dependencies resolution needs, as swappable
// function fields. This is the same package-seam pattern internal/secrets
// already uses (secrets.go:50-53) and it is why no test here touches the
// real Keychain, real gh, or the network.
type Host struct {
	ReadKeychain    func(service string) (string, error)
	ReadSecretsFile func(path string) (map[string]string, error)
	LookupEnv       func(key string) (string, bool)
	DiscoverClaude  func() (string, time.Time, error)
	DiscoverGh      func() (string, error)
}

// Resolve returns the ranked candidate stack for every cspace-owned key.
// It collects; it does not choose. Winner() chooses, and Bake's 401 ladder
// re-chooses — which is why the losing candidates must survive.
//
// A project's compose env_file and devcontainer containerEnv are absent by
// construction: they are not sources here, so they cannot shadow anything.
func (h Host) Resolve(project, projectRoot, userHome string, envFlags map[string]string) map[string]Resolution {
	out := make(map[string]Resolution, len(Keys()))

	projectFile := h.secretsFile(projectRoot)
	userFile := h.secretsFile(userHome)
	claudeTok, claudeExp := h.claude()
	ghTok := h.gh()

	for _, key := range Keys() {
		r := Resolution{Key: key}

		if v, ok := envFlags[key]; ok && v != "" {
			r.add(key, v, SourceEnvFlag, "--env "+key, time.Time{})
		}
		if project != "" {
			svc := "cspace-" + project + "-" + key
			if v := h.keychain(svc); v != "" {
				r.add(key, v, SourceProjectKeychain, svc, time.Time{})
			}
		}
		if svc := "cspace-" + key; true {
			if v := h.keychain(svc); v != "" {
				r.add(key, v, SourceGlobalKeychain, svc, time.Time{})
			}
		}
		if v := projectFile[key]; v != "" {
			r.add(key, v, SourceLegacyProjectFile, filepath.Join(projectRoot, ".cspace", "secrets.env"), time.Time{})
		}
		if v := userFile[key]; v != "" {
			r.add(key, v, SourceLegacyUserFile, filepath.Join(userHome, ".cspace", "secrets.env"), time.Time{})
		}
		if h.LookupEnv != nil {
			if v, ok := h.LookupEnv(key); ok && v != "" {
				r.add(key, v, SourceHostShell, "$"+key, time.Time{})
			}
		}
		switch key {
		case KeyAnthropicAPIKey, KeyClaudeOAuthToken:
			// Auto-discovery fills only the OAuth carrier; the Anthropic
			// blob is always an sk-ant-oat access token.
			if key == KeyClaudeOAuthToken && claudeTok != "" {
				r.add(key, claudeTok, SourceAutoDiscovered, "Claude Code-credentials", claudeExp)
			}
		case KeyGHToken, KeyGitHubToken, KeyGitHubPAT:
			if ghTok != "" {
				r.add(key, ghTok, SourceAutoDiscovered, "gh auth token", time.Time{})
			}
		}

		out[key] = r
	}
	return out
}

func (r *Resolution) add(key, value string, src Source, detail string, exp time.Time) {
	r.Candidates = append(r.Candidates, Credential{
		Key: key, Value: value, Source: src, Detail: detail, ExpiresAt: exp,
	})
}

func (h Host) keychain(service string) string {
	if h.ReadKeychain == nil {
		return ""
	}
	v, err := h.ReadKeychain(service)
	if err != nil {
		return ""
	}
	return v
}

func (h Host) secretsFile(dir string) map[string]string {
	if h.ReadSecretsFile == nil || dir == "" {
		return nil
	}
	m, err := h.ReadSecretsFile(filepath.Join(dir, ".cspace", "secrets.env"))
	if err != nil {
		return nil
	}
	return m
}

func (h Host) claude() (string, time.Time) {
	if h.DiscoverClaude == nil {
		return "", time.Time{}
	}
	tok, exp, err := h.DiscoverClaude()
	if err != nil {
		return "", time.Time{}
	}
	return tok, exp
}

func (h Host) gh() string {
	if h.DiscoverGh == nil {
		return ""
	}
	tok, err := h.DiscoverGh()
	if err != nil {
		return ""
	}
	return tok
}
```

Note the deliberate omission: a **Keychain read error is swallowed here** and must not be. Task 3b (next step) fixes it — the spec requires a hard error for a locked keychain, distinguishable from a miss.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/credentials/ -v`
Expected: PASS.

- [ ] **Step 5: Add the locked-keychain test and fix the swallowed error**

Append to `resolve_test.go`:

```go
func TestResolveSurfacesLockedKeychainAsError(t *testing.T) {
	// A miss is `security` exit 44 and means "not set" — ReadKeychain
	// already maps that to ("", nil). A real error means the keychain is
	// locked, and reporting that as "not set" makes a locked keychain
	// look empty.
	h := newTestHost()
	h.ReadKeychain = func(string) (string, error) { return "", errLocked }
	if _, err := h.ResolveErr("proj", "/p", "/home/u", nil); err == nil {
		t.Fatal("want error when the keychain read fails for real")
	}
}

var errLocked = errKeychain("keychain is locked")

type errKeychain string

func (e errKeychain) Error() string { return string(e) }
```

Change `Resolve` to delegate to a new `ResolveErr` that returns `(map[string]Resolution, error)`, propagating any non-nil error from `ReadKeychain`; keep `Resolve` as the error-swallowing convenience wrapper used only where a failure is already fatal. Replace `func (h Host) keychain(service string) string` with a variant that records the first error on the `Host` call, or thread an error through — implementer's choice, but `ResolveErr` must return non-nil when `ReadKeychain` does.

- [ ] **Step 6: Run tests and `make check`**

Run: `go test ./internal/credentials/ -v && make fmt-check vet test`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/credentials/resolve.go internal/credentials/resolve_test.go
git commit -m "Add Resolve: ranked credential candidates from all six sources

Collects every candidate rather than picking one, so Bake's 401 ladder
has fallback rungs. Project Keychain outranks global; ambient host shell
now ranks below Keychain so a stale export cannot shadow a scoped token.
A locked keychain is an error, not an empty result.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Verify — a pure liveness predicate

**Files:**
- Create: `internal/credentials/verify.go`
- Test: `internal/credentials/verify_test.go`
- Reference: `internal/secrets/github.go:78-95` (`ReconcileGitHubToken` — the coupling being undone)

**Interfaces:**
- Consumes: `Credential` (Task 1)
- Produces: `type Validity int` (`ValidityUnknown`, `ValidityValid`, `ValidityRejected`), `func (h Host) Verify(c Credential) Validity`, plus `Host.VerifyGitHub func(token string) Validity`

`ReconcileGitHubToken` today couples verification with fallback discovery (`github.go:88`). The port splits them: `Verify` answers only "is this accepted"; the ladder lives in `Bake` (Task 5).

- [ ] **Step 1: Write the failing test**

```go
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
			got := h.Verify(Credential{Key: KeyGHToken, Value: "t"})
			if got != tt.want {
				t.Fatalf("Verify() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVerifyAnthropicIsNotNetworkChecked(t *testing.T) {
	// Boot must not make an Anthropic API call. Expiry is the only signal
	// on that side, and it is already carried on the Credential.
	h := newTestHost()
	h.VerifyGitHub = func(string) Validity { t.Fatal("must not call GitHub for an Anthropic key"); return ValidityUnknown }
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/credentials/ -run TestVerify -v`
Expected: FAIL — `undefined: ValidityValid`.

- [ ] **Step 3: Write minimal implementation**

```go
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

// Verify is a pure predicate: it answers only "is this credential
// accepted", never "what should replace it". Replacement is Bake's ladder.
//
// Only GitHub is network-checked. An Anthropic call would cost a token on
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
```

Add `VerifyGitHub func(token string) Validity` to the `Host` struct in `resolve.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/credentials/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/credentials/verify.go internal/credentials/verify_test.go internal/credentials/resolve.go
git commit -m "Add Verify as a pure liveness predicate

ReconcileGitHubToken couples verification with fallback discovery; this
splits them so the ladder can live in Bake and run against the value that
actually ships. A network failure stays Unknown and never downgrades a
credential.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Bake — merge order, policy, and the 401 fallback ladder

**Files:**
- Create: `internal/credentials/bake.go`
- Test: `internal/credentials/bake_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1-4
- Produces:
  - `type BakeResult struct { Env map[string]string; Winners map[string]Credential; Shadowed []string; Validities map[string]Validity }`
  - `func (h Host) Bake(res map[string]Resolution, appEnv map[string]string) BakeResult`

This is the task that closes failure #2. `appEnv` arrives with the compose `env_file` and `containerEnv` already merged; Bake strips the five keys from it (recording them as `Shadowed` for reporting) and writes cspace's own values.

- [ ] **Step 1: Write the failing test**

```go
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

	if got.Env["GH_TOKEN"] != "cspace-token" {
		t.Errorf("GH_TOKEN = %q, want cspace's value to win", got.Env["GH_TOKEN"])
	}
	if got.Env["RESEND_API_KEY"] != "app-var" {
		t.Error("app vars must pass through untouched")
	}
	if len(got.Shadowed) != 1 || got.Shadowed[0] != "GH_TOKEN" {
		t.Errorf("Shadowed = %v, want [GH_TOKEN]", got.Shadowed)
	}
}

func TestBakeIgnoresEnvFileEvenWhenCspaceResolvesNothing(t *testing.T) {
	// Decision 3 is unconditional. This is the accepted breakage: a project
	// whose .env was the only source now boots with no credential.
	h := newTestHost()
	got := h.Bake(map[string]Resolution{}, map[string]string{"GH_TOKEN": "only-source"})
	if _, present := got.Env["GH_TOKEN"]; present {
		t.Fatalf("GH_TOKEN = %q, want it absent", got.Env["GH_TOKEN"])
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
	if got.Env["GH_TOKEN"] != "good" {
		t.Fatalf("GH_TOKEN = %q, want the ladder to advance to the valid rung", got.Env["GH_TOKEN"])
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
	if got := h.Bake(res, nil); got.Env["GH_TOKEN"] != "first" {
		t.Fatalf("GH_TOKEN = %q, want the offline case to keep the top rung", got.Env["GH_TOKEN"])
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/credentials/ -run TestBake -v`
Expected: FAIL — `undefined: BakeResult`.

- [ ] **Step 3: Write minimal implementation**

```go
package credentials

import "sort"

// BakeResult is the outcome of applying credential policy to a container's
// environment.
type BakeResult struct {
	Env        map[string]string     // the final container env
	Winners    map[string]Credential // which credential shipped per key
	Shadowed   []string              // cspace-owned keys a project file set and lost
	Validities map[string]Validity   // Verify outcome per shipped key
}

// Bake produces the final container environment.
//
// appEnv arrives with the project's compose env_file and devcontainer
// containerEnv already merged. The five cspace-owned keys are stripped from
// it — unconditionally, including when cspace resolves nothing — and
// replaced with cspace's own resolution. That is what makes the guarantee
// "a project env_file cannot shadow a cspace credential" absolute rather
// than best-effort.
//
// Verification runs on the value that actually ships, not on the value
// resolution first picked. A rejected credential advances down the ladder;
// an unverifiable one does not.
func (h Host) Bake(res map[string]Resolution, appEnv map[string]string) BakeResult {
	out := BakeResult{
		Env:        make(map[string]string, len(appEnv)+len(Keys())),
		Winners:    make(map[string]Credential),
		Validities: make(map[string]Validity),
	}
	for k, v := range appEnv {
		if IsOwnedKey(k) {
			out.Shadowed = append(out.Shadowed, k)
			continue
		}
		out.Env[k] = v
	}
	sort.Strings(out.Shadowed)

	picked := make(map[string]Credential)
	for _, key := range Keys() {
		c, v, ok := h.climb(res[key])
		if !ok {
			continue
		}
		picked[key] = c
		out.Validities[key] = v
	}

	for k, c := range ApplyGroupPolicy(picked) {
		out.Env[k] = c.Value
		out.Winners[k] = c
	}
	return out
}

// climb walks the candidate stack, stopping at the first credential the
// provider does not definitively reject. ValidityUnknown stops the climb:
// an offline check is not evidence of a bad token.
func (h Host) climb(r Resolution) (Credential, Validity, bool) {
	for _, c := range r.Candidates {
		v := h.Verify(c)
		if v != ValidityRejected {
			return c, v, true
		}
	}
	// Every rung rejected: ship the top one anyway and let reporting say so.
	if c, ok := r.Winner(); ok {
		return c, ValidityRejected, true
	}
	return Credential{}, ValidityUnknown, false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/credentials/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/credentials/bake.go internal/credentials/bake_test.go
git commit -m "Add Bake: strip project env_file credentials, verify what ships

The five cspace keys are stripped from the app env unconditionally and
replaced with cspace's resolution, so a project .env can no longer shadow
a credential. Verification runs after the merge rather than 176 lines
before it, which is how a dead PAT reached a container unvalidated.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Inspect and divergence — fixture first

**Files:**
- Create: `internal/credentials/inspect.go`
- Create: `internal/credentials/testdata/inspect-1.2.json`
- Test: `internal/credentials/inspect_test.go`

**Interfaces:**
- Consumes: `Credential`, `Keys()` (Tasks 1-2)
- Produces: `func ParseBakedEnv(inspectJSON []byte) (map[string]string, error)`, `func Diverged(baked map[string]string, winners map[string]Credential) []string`

- [ ] **Step 1: Capture a real fixture BEFORE writing any parsing code**

The init-process env JSON path is referenced nowhere in the repo — the current `inspectRecord` parses only `status.networks` (`adapter.go:417-424`), and `adapter.go:4-11` documents that this CLI reshapes its JSON across versions. The whole `doctor` container section rests on this path, so capture it from a live 1.2 host rather than assuming it.

```bash
container ls --all --format json | head -40   # find a running cspace-* sandbox
container inspect <sandbox-name> > /tmp/inspect-raw.json
```

Redact every secret value before committing the fixture — replace each credential value with a recognizable placeholder like `BAKED_GH_TOKEN_VALUE`. Keep the JSON **shape** byte-for-byte otherwise. Save as `internal/credentials/testdata/inspect-1.2.json`.

If the observed path differs from `configuration.initProcess.environment`, update the implementation in Step 3 to match the fixture — the fixture wins, not this plan.

- [ ] **Step 2: Write the failing test**

```go
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
	if env["GH_TOKEN"] == "" {
		t.Fatalf("want GH_TOKEN in the baked env, got keys: %v", keysOf(env))
	}
}

func TestDivergedComparesBakedAgainstResolved(t *testing.T) {
	baked := map[string]string{"GH_TOKEN": "old", "ANTHROPIC_API_KEY": "same"}
	winners := map[string]Credential{
		"GH_TOKEN":          {Key: "GH_TOKEN", Value: "new"},
		"ANTHROPIC_API_KEY": {Key: "ANTHROPIC_API_KEY", Value: "same"},
	}
	got := Diverged(baked, winners)
	if len(got) != 1 || got[0] != "GH_TOKEN" {
		t.Fatalf("Diverged() = %v, want [GH_TOKEN]", got)
	}
}

func TestDivergedIgnoresKeysCspaceNoLongerResolves(t *testing.T) {
	// A container holding a credential cspace no longer resolves is a
	// missing-credential story, not a divergence story. Reporting both
	// would double-count it.
	baked := map[string]string{"GH_TOKEN": "old"}
	if got := Diverged(baked, map[string]Credential{}); len(got) != 0 {
		t.Fatalf("Diverged() = %v, want empty", got)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/credentials/ -run 'TestParseBakedEnv|TestDiverged' -v`
Expected: FAIL — `undefined: ParseBakedEnv`.

- [ ] **Step 4: Write minimal implementation**

```go
package credentials

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ParseBakedEnv extracts a container's init-process environment from
// `container inspect` output. Credentials are baked in at create time and
// immutable for the container's life, so this — not host re-resolution —
// is the only honest answer to "what credential does this sandbox have".
//
// The JSON path is pinned by testdata/inspect-1.2.json. Apple Container
// reshapes its inspect output across versions (see adapter.go:4-11); if a
// future release moves this, the fixture test fails loudly rather than
// silently reporting an empty env.
func ParseBakedEnv(inspectJSON []byte) (map[string]string, error) {
	var records []struct {
		Configuration struct {
			InitProcess struct {
				Environment []string `json:"environment"`
			} `json:"initProcess"`
		} `json:"configuration"`
	}
	if err := json.Unmarshal(inspectJSON, &records); err != nil {
		// 1.1.x pretty-prints and some paths emit a bare object.
		var one struct {
			Configuration struct {
				InitProcess struct {
					Environment []string `json:"environment"`
				} `json:"initProcess"`
			} `json:"configuration"`
		}
		if err2 := json.Unmarshal(inspectJSON, &one); err2 != nil {
			return nil, fmt.Errorf("parse container inspect: %w", err)
		}
		records = append(records, one)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("container inspect returned no records")
	}
	out := map[string]string{}
	for _, e := range records[0].Configuration.InitProcess.Environment {
		k, v, found := strings.Cut(e, "=")
		if !found {
			continue
		}
		out[k] = v
	}
	return out, nil
}

// Diverged lists cspace-owned keys whose baked value differs from what the
// host resolves now. Keys cspace no longer resolves are excluded: that is a
// missing-credential report, not a divergence one.
func Diverged(baked map[string]string, winners map[string]Credential) []string {
	var out []string
	for _, key := range Keys() {
		w, ok := winners[key]
		if !ok || w.Value == "" {
			continue
		}
		b, present := baked[key]
		if !present || b != w.Value {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/credentials/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/credentials/inspect.go internal/credentials/inspect_test.go internal/credentials/testdata/inspect-1.2.json
git commit -m "Add Inspect and divergence detection against a real 1.2 fixture

Reads a container's baked init-process env so doctor can report what a
sandbox actually holds rather than what the host would resolve now. The
fixture is captured from a live Apple Container 1.2 host because this JSON
path is referenced nowhere else in the tree and the CLI reshapes its
output across versions.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Config — `credentials.runwayWarningHours`

**Files:**
- Modify: `internal/config/config.go` (add to the `Config` struct at line 27)
- Modify: `lib/defaults.json` (seed the default)
- Test: `internal/config/config_test.go` (append)

**Interfaces:**
- Consumes: nothing
- Produces: `cfg.Credentials.RunwayWarningHours int`

The default **must** be seeded in `lib/defaults.json`, not applied in Go: `DeepMerge` (`config.go:104-153`) is a JSON round-trip in which an absent field and an explicit `0` are indistinguishable for a plain `int`, and `0` is a meaningful value (disables escalation).

- [ ] **Step 1: Write the failing test**

```go
func TestCredentialsRunwayDefaultIsSeededInDefaults(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Credentials.RunwayWarningHours != 4 {
		t.Fatalf("RunwayWarningHours = %d, want the seeded default of 4", cfg.Credentials.RunwayWarningHours)
	}
}

func TestCredentialsRunwayZeroDisablesEscalation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".cspace.json"),
		[]byte(`{"credentials":{"runwayWarningHours":0}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Credentials.RunwayWarningHours != 0 {
		t.Fatalf("RunwayWarningHours = %d, want an explicit 0 to survive the merge", cfg.Credentials.RunwayWarningHours)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestCredentialsRunway -v`
Expected: FAIL — `cfg.Credentials undefined`.

- [ ] **Step 3: Implement**

In `internal/config/config.go`, add to the `Config` struct:

```go
	Credentials CredentialsConfig `json:"credentials,omitempty"`
```

and the type:

```go
// CredentialsConfig tunes credential reporting. RunwayWarningHours is the
// threshold below which a short-lived credential escalates from the boot
// summary line to a warning; 0 disables the escalation. The default is
// seeded in defaults.json rather than applied here, because DeepMerge is a
// JSON round-trip that cannot distinguish an absent int from an explicit 0.
type CredentialsConfig struct {
	RunwayWarningHours int `json:"runwayWarningHours"`
}
```

In `lib/defaults.json`, add a top-level key:

```json
  "credentials": {
    "runwayWarningHours": 4
  },
```

- [ ] **Step 4: Sync embedded assets and run tests**

Run: `make sync-embedded && go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go lib/defaults.json internal/config/config_test.go
git commit -m "Add credentials.runwayWarningHours config with a seeded default

Seeded in defaults.json rather than applied in Go: DeepMerge is a JSON
round-trip, so an absent int and an explicit 0 are indistinguishable, and
0 meaningfully disables the escalation.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: (intentionally empty — see Global Constraints)

No overlay changes. Credential work moves **before** `overlay.Start`, so the summary line prints to the real terminal and the overlay is untouched. The summary line ships in Task 10. This task number is retained so the plan's numbering matches the spec review's references.

---

### Task 9: Render — summary line and doctor sections

**Files:**
- Create: `internal/credentials/render.go`
- Test: `internal/credentials/render_test.go`

**Interfaces:**
- Consumes: `BakeResult`, `Credential`, `Durability` (Tasks 1-5)
- Produces: `func SummaryLine(b BakeResult, now time.Time, runway time.Duration) (line string, warning string)`

- [ ] **Step 1: Write the failing test**

```go
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
		Shadowed: []string{"GH_TOKEN", "GITHUB_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"},
	}
	line, _ := SummaryLine(b, time.Now(), 4*time.Hour)
	if !strings.Contains(line, "project env") {
		t.Errorf("line = %q, want a compact note that a project file lost", line)
	}
	if strings.Contains(line, "GITHUB_TOKEN") {
		t.Errorf("line = %q, must not enumerate keys — doctor carries the detail", line)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/credentials/ -run TestSummaryLine -v`
Expected: FAIL — `undefined: SummaryLine`.

- [ ] **Step 3: Implement**

Write `SummaryLine` to produce, for example:

```
credentials: claude ← keychain (durable) · github ← keychain:resume-redux (verified)
```

Rules the tests pin:
- one segment per shipping group (Anthropic → `claude`, GitHub → `github`), skipping absent groups
- each segment names `Source.String()` and either the durability word or `verified`/`rejected` from `Validities`
- when `len(b.Shadowed) > 0`, append ` · project env ignored for cspace keys` — never the key names
- the warning is non-empty only when some winner is `Expiring`/`Expired` and `runway > 0`, or some validity is `ValidityRejected`
- for a project-scoped Keychain win, render `keychain:<project>` by taking the segment of `Detail` between `cspace-` and `-<KEY>`

- [ ] **Step 4: Run tests**

Run: `go test ./internal/credentials/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/credentials/render.go internal/credentials/render_test.go
git commit -m "Render the credential summary line and its escalation

Always-on information rather than an alarm: carrier, source, and
durability on one line, escalating only inside the runway threshold or on
a 401. Shadowed keys are noted compactly without enumeration; doctor
carries the detail.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 10: Wire `cspace up`

**Files:**
- Modify: `internal/cli/cmd_up.go` — remove lines ~122-178 (preflight + carrier dedup twin + `ReconcileGitHubToken` call), ~356-368 (`envFileSecretCollisions` warning), ~480-531 (carrier dedup + `propagateFamily` ×2)
- Delete: `internal/cli/cmd_up_secret_warnings.go` and `internal/cli/cmd_up_secret_warnings_test.go`
- Modify: `internal/secrets/secrets.go` — delete `normalizeAnthropicCarrier`, `autoDiscover`, `cspaceKeys`, `SecretKeys`; keep `parse`, `mergeFile`, `loadFromDirs`, `OAuthExpired`, `timeNow`
- Modify: `internal/secrets/github.go` — delete `ReconcileGitHubToken`; export the validator as `ValidateGitHubToken(token string) Validity`-shaped for `Host.VerifyGitHub`

**Interfaces:**
- Consumes: `credentials.Host`, `Resolve`, `Bake`, `SummaryLine` (Tasks 3-9)
- Produces: nothing downstream

- [ ] **Step 1: Build the production Host and resolve before the overlay**

In `cmd_up.go`, replacing the block at ~107-178, before `overlay.Start` at line 222:

```go
			// All credential work happens BEFORE the overlay starts, so its
			// output reaches the real terminal. The overlay redirects stdout
			// into a pending buffer; anything written after overlay.Start is
			// shredded mid-render. cspace's resolution no longer depends on
			// the compose env_file merge, so nothing forces it later.
			credHost := credentials.ProductionHost()
			resolved, err := credHost.ResolveErr(project, projectRoot, home, envFlagCredentials(extraEnv))
			if err != nil {
				return fmt.Errorf("resolve credentials: %w", err)
			}
```

Add `credentials.ProductionHost()` in a new `internal/credentials/production.go`, wiring the real `secrets.ReadKeychain`, a dotenv reader over `secrets`' parser, `os.LookupEnv`, `secrets.DiscoverClaudeOauthToken`, `secrets.DiscoverGhAuthToken`, and the GitHub validator.

`envFlagCredentials(extraEnv)` extracts only the five owned keys from the `--env` slice, so they enter as `SourceEnvFlag` rather than being stripped by `Bake`.

- [ ] **Step 2: Bake after the app env is assembled**

At the point where `env` is complete (after the compose `env_file` merge and `containerEnv` merge, replacing lines ~480-531):

```go
			baked := credHost.Bake(resolved, env)
			env = baked.Env
```

- [ ] **Step 3: Print the summary before the overlay**

Immediately after resolution in Step 1 — note this must run before `overlay.Start`, so compute `Bake` early enough or split reporting from baking. Simplest correct order: assemble app env, `Bake`, print summary, *then* `overlay.Start`. Move `overlay.Start` down accordingly, keeping it before `PhaseDaemon`.

```go
			runway := time.Duration(cfg.Credentials.RunwayWarningHours) * time.Hour
			line, warn := credentials.SummaryLine(baked, time.Now(), runway)
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), line)
			if warn != "" {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+warn)
			}
```

- [ ] **Step 4: Run the full suite**

Run: `make fmt-check vet test`
Expected: PASS. Tests referencing `SecretKeys`, `envFileSecretCollisions`, `ReconcileGitHubToken`, or `normalizeAnthropicCarrier` must be deleted or ported — they test behavior that no longer exists.

- [ ] **Step 5: Verify against a real sandbox**

```bash
make build
./bin/cspace-go up credtest --no-browser
```

Expected: the credentials line prints before the overlay, un-garbled, and the boot reaches ready. Then confirm the container did **not** receive the project `.env` values:

```bash
container inspect cspace-<project>-credtest | grep -c 'GH_TOKEN'
./bin/cspace-go down credtest
```

- [ ] **Step 6: Commit**

```bash
git add -A internal/cli internal/secrets internal/credentials
git commit -m "Wire cspace up to the credentials package

Resolution, baking, and reporting all run before overlay.Start, so the
summary reaches the real terminal instead of being shredded mid-render.
Deletes the preflight carrier-dedup twin, envFileSecretCollisions,
propagateFamily, and ReconcileGitHubToken — all replaced by Bake.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 11: Wire `cspace keychain` — delete the second implementation, add `--project`

**Files:**
- Modify: `internal/cli/cmd_keychain.go` — delete `credentialSource` (line 209) and `hasKey`; rewrite `status` over `Resolve`; add `--project` to `init`
- Test: `internal/cli/cmd_keychain_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestKeychainInitProjectRefusesDerivedProjectName(t *testing.T) {
	// projectName() falls back to the directory basename and then to the
	// literal "default" (cmd_up.go:1304-1312, config.go:226-229). Writing
	// cspace-default-<KEY> would create an entry shared by every unnamed
	// project on the host — the opposite of least privilege.
	err := validateProjectScope("", false)
	if err == nil {
		t.Fatal("want an error when the project name is not explicit")
	}
}

func TestKeychainInitProjectAcceptsExplicitName(t *testing.T) {
	if err := validateProjectScope("resume-redux", true); err != nil {
		t.Fatalf("validateProjectScope() = %v, want nil", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestKeychainInitProject -v`
Expected: FAIL — `undefined: validateProjectScope`.

- [ ] **Step 3: Implement**

```go
// validateProjectScope guards `cspace keychain init --project`. The project
// name must be explicit — set in .cspace.json's project.name or passed by
// flag — never derived from the directory basename, which silently detaches
// the scoped entry when a folder is renamed and drops resolution to the
// broader global credential.
func validateProjectScope(name string, explicit bool) error {
	if name == "" || !explicit || name == "default" {
		return fmt.Errorf("`keychain init --project` requires an explicit project name: " +
			"set project.name in .cspace.json or pass --project-name")
	}
	return nil
}
```

Rewrite `status` to call `credentials.ProductionHost().Resolve(...)` and render each key's winner and fallbacks. Delete `credentialSource` and `hasKey` entirely.

- [ ] **Step 4: Run tests and the real command**

Run: `make fmt-check vet test && make build && ./bin/cspace-go keychain status`
Expected: PASS, and `status` no longer shows `ANTHROPIC_API_KEY` and `CLAUDE_CODE_OAUTH_TOKEN` both sourced from auto-discovery — that state is now unrepresentable.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/cmd_keychain.go internal/cli/cmd_keychain_test.go
git commit -m "Delete credentialSource; keychain status now shares boot's resolution

credentialSource re-walked all four layers by hand without carrier
exclusivity or env_file awareness, so status reported states Load() could
not produce. Adds keychain init --project, refusing a derived project name
because a basename-keyed entry detaches silently on rename.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 12: Wire `cspace doctor` — host and container sections

**Files:**
- Modify: `internal/cli/probes.go` — replace `credentialProbeCheck`/`credentialSource` usage with `credentials`; add the container section
- Test: `internal/cli/probes_test.go`

**Interfaces:**
- Consumes: `Resolve`, `ParseBakedEnv`, `Diverged`, `Verify` (Tasks 3-6)

- [ ] **Step 1: Write the failing test**

```go
func TestCredentialProbeReportsDivergedContainer(t *testing.T) {
	checks := credentialContainerChecks(
		"mercury",
		map[string]string{"GH_TOKEN": "old"},
		map[string]credentials.Credential{"GH_TOKEN": {Key: "GH_TOKEN", Value: "new"}},
		credentials.ValidityRejected,
	)
	if len(checks) == 0 || checks[0].Status != ProbeFail {
		t.Fatalf("checks = %+v, want a failing check for a diverged container", checks)
	}
	joined := checks[0].Title + strings.Join(checks[0].Details, " ")
	if !strings.Contains(joined, "DIVERGED") {
		t.Errorf("want the divergence named, got %q", joined)
	}
	if !strings.Contains(joined, "down") || !strings.Contains(joined, "up") {
		t.Errorf("want the down && up remedy, got %q", joined)
	}
}

func TestCredentialProbeExcludesSidecars(t *testing.T) {
	// Sidecars and the browser sidecar legitimately carry different env;
	// enumerating them would report every one as DIVERGED.
	names := []string{"cspace-proj-mercury", "cspace-proj-browser", "cspace-proj-mercury-convex-backend"}
	got := workspaceSandboxNames("proj", names, []string{"mercury"})
	if len(got) != 1 || got[0] != "cspace-proj-mercury" {
		t.Fatalf("workspaceSandboxNames() = %v, want only the registry-tracked sandbox", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestCredentialProbe -v`
Expected: FAIL — `undefined: credentialContainerChecks`.

- [ ] **Step 3: Implement**

`workspaceSandboxNames` filters candidate container names to those matching registry-tracked sandbox names for the project — never compose sidecars, never `cspace-<project>-browser`.

`credentialContainerChecks` compares baked vs resolved and emits:
- `ProbePass` when the baked value matches and verifies
- `ProbeFail` with `DIVERGED` plus the `cspace down <name> && cspace up <name>` remedy when values differ
- `ProbeFail` with `rejected (401)` when `Verify` rejects the baked value
- `ProbeWarn` when validity is `Unknown`

When the substrate is unreachable, emit a single `ProbeWarn` noting the container section is unavailable and return host-only checks rather than erroring.

- [ ] **Step 4: Run tests**

Run: `make fmt-check vet test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/probes.go internal/cli/probes_test.go
git commit -m "Make doctor inspect running sandboxes, not just host resolution

doctor previously re-resolved on the host and reported the source it would
pick now, so it showed a green check while a container held a dead token
from a different source. It now reads each registry-tracked sandbox's
baked env and reports divergence and rejection separately. Sidecars are
excluded — they legitimately carry different env.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 13: Guard `extractCredentials` against the five keys

**Files:**
- Modify: `internal/devcontainer/validate.go:32-36`
- Test: `internal/devcontainer/validate_test.go`

`cspace-entrypoint.sh:44-51` sources `/sessions/extracted.env` at runtime, and `model.go:65` imposes no key restrictions — so an extraction naming a cspace key would shadow the baked credential seconds after Bake guarantees it cannot be shadowed.

- [ ] **Step 1: Write the failing test**

```go
func TestValidateRejectsExtractCredentialsTargetingCspaceKeys(t *testing.T) {
	for _, key := range []string{"GH_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"} {
		c := &Config{}
		c.Customizations.Cspace.ExtractCredentials = []ExtractCredential{
			{From: "host", Exec: []string{"echo", "x"}, Env: key},
		}
		err := Validate(c)
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("Validate() for %s = %v, want an error naming the key", key, err)
		}
	}
}

func TestValidateAllowsExtractCredentialsForAppKeys(t *testing.T) {
	c := &Config{}
	c.Customizations.Cspace.ExtractCredentials = []ExtractCredential{
		{From: "host", Exec: []string{"echo", "x"}, Env: "CONVEX_DEPLOY_KEY"},
	}
	if err := Validate(c); err != nil {
		t.Fatalf("Validate() = %v, want nil for a non-cspace key", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/devcontainer/ -run TestValidate -v`
Expected: FAIL — no error returned for `GH_TOKEN`.

- [ ] **Step 3: Implement**

In `validate.go`, inside the existing loop at line 32:

```go
		if credentials.IsOwnedKey(ec.Env) {
			return fmt.Errorf("devcontainer.json: extractCredentials may not target %s — "+
				"cspace owns that variable and injects it at container create. "+
				"Store the credential with `cspace keychain init` instead", ec.Env)
		}
```

If importing `internal/credentials` from `internal/devcontainer` creates a cycle, move `Keys()`/`IsOwnedKey` into a tiny leaf package both can import.

- [ ] **Step 4: Run tests**

Run: `make fmt-check vet test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/devcontainer/
git commit -m "Reject extractCredentials entries targeting cspace-owned keys

The entrypoint sources /sessions/extracted.env at runtime, so an
extraction naming one of the five keys would shadow the baked credential
seconds after Bake guarantees it cannot be shadowed.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 14: Docs and findings

**Files:**
- Modify: `CLAUDE.md` — the "Anthropic credentials" and "Env plumbing" sections
- Modify: `docs/env-cspace.md` — the precedence section
- Create: six finding files in `.cspace/context/findings/`

- [ ] **Step 1: Update `CLAUDE.md`**

Rewrite the credentials resolution order to the six ranks, state that compose `env_file` and `containerEnv` are ignored for the five keys, document `cspace-<project>-<KEY>` scoping, and remove the now-false claim that `env_file` entries out-rank `.cspace/secrets.env`.

- [ ] **Step 2: Update `docs/env-cspace.md`**

Replace the "Precedence (stated honestly)" section with the new table. Add the three migration breakages verbatim from the spec's Migration section — case 3 (app code reading `ANTHROPIC_API_KEY` from its own `.env`) especially, since there is no opt-out.

- [ ] **Step 3: Log the findings**

One file each, frontmatter per `CLAUDE.md`'s findings convention (`title`, `date: 2026-08-17`, `kind: finding`, `status`, `category`, `tags`):

1. `status: resolved` — overlay-ordering: credential warnings after `overlay.Start` are shredded
2. `status: resolved` — `ReconcileGitHubToken` validated a value the later `env_file` merge discarded
3. `status: resolved` — `propagateFamily` clobbered an explicitly-set `GITHUB_PERSONAL_ACCESS_TOKEN`
4. `status: resolved` — `doctor` reported host resolution while claiming to describe sandbox health
5. `status: open` — sidecars receive `env_file` credentials outside any cspace policy (out of scope; `sidecars/lifecycle.go:31`)
6. `status: open` — `up` against a running container re-registers a fresh control token, breaking `send`/`agent` (`cmd_up.go:795-807`)

Append a resolved entry to `2026-07-16-env-precedence-smeared-env-flag-loses-to-ambient-credentials.md`.

- [ ] **Step 4: Full check**

Run: `make check`
Expected: PASS (or `fmt-check vet test` if `golangci-lint` is not installed locally).

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md docs/env-cspace.md .cspace/context/findings/
git commit -m "Document the new credential precedence and log six findings

Records the three accepted migration breakages, including app code that
reads ANTHROPIC_API_KEY from its own .env — that case has no opt-out and
belongs in release notes, not only the spec.

(cs-finding:env-precedence-smeared-env-flag-loses-to-ambient-credentials)

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:** Decisions 1-8 all map to tasks — 1/6 → Tasks 1-5, 11; 2 → Task 3; 3 → Task 5; 4/5 → Tasks 7, 9, 10; 7 → Tasks 3, 11; 8 → Tasks 3, 11 (non-darwin assertions). Data model → Task 1. Key groups → Task 2. Merge order → Tasks 5, 10. Precedence → Task 3. Behavior changes 1-4 → Tasks 3, 5. Output surfaces → Tasks 9-12. Error handling → Tasks 3 (locked keychain), 4 (Unknown), 5 (ladder), 12 (substrate degradation). Project identity → Task 11. `extracted.env` → Task 13. Migration + findings → Task 14. Testing → distributed across every task.

**Known gap, deliberate:** the spec's `PhaseCredentials` overlay work is replaced by the pre-overlay approach documented in Global Constraints. Task 8 is empty by design.

**Type consistency:** `Host` is defined in Task 3 and extended with `VerifyGitHub` in Task 4 — both noted. `Validity` (Task 4) is used by `BakeResult.Validities` (Task 5), `SummaryLine` (Task 9), and `credentialContainerChecks` (Task 12) under the same name. `Credential`/`Resolution`/`Durability` names are stable from Task 1 onward. `Keys()`/`IsOwnedKey` (Task 2) are used in Tasks 5, 6, 13.
