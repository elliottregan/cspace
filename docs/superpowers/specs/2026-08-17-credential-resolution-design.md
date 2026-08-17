# Credential resolution: one owner, explicit precedence, container-aware reporting

- **Date:** 2026-08-17
- **Status:** approved, not yet implemented
- **Revision:** 2 — incorporates adversarial spec review (2026-08-17)
- **Supersedes:** the credential-resolution behavior currently spread across
  `internal/secrets`, `internal/cli/cmd_up.go`, `internal/cli/cmd_keychain.go`,
  and `internal/cli/probes.go`

## Problem

cspace's credential handling works, but it is not owned by anything. Resolution
logic is implemented three separate times, the implementations have drifted, and
the drift produces wrong answers that read as healthy ones.

Three concrete failures motivated this design. All three were reproduced on a
live host on 2026-08-17.

### 1. `cspace keychain status` reports a state that cannot exist

`credentialSource()` (`cmd_keychain.go:209`) re-walks all four resolution layers
by hand — re-reading the files, re-reading the Keychain, re-running discovery.
It does not apply `normalizeAnthropicCarrier`, it does not apply the carrier
exclusivity that `cmd_up.go:496-513` enforces, and it knows nothing about the
compose `env_file` merge. `probes.go:430` consumes it, so `doctor` inherits
every one of those gaps.

Observed output:

```
ANTHROPIC_API_KEY        source: auto-discovered (Claude Code OAuth)
CLAUDE_CODE_OAUTH_TOKEN  source: auto-discovered (Claude Code OAuth)
```

`autoDiscover()` only ever fills `CLAUDE_CODE_OAUTH_TOKEN` (`secrets.go:190`),
and `normalizeAnthropicCarrier` never moves an `sk-ant-oat` value *into*
`ANTHROPIC_API_KEY` (`secrets.go:151-156`). No code path produces that first
row. Neither row mentions that the sandbox actually received its token from the
project's `.env`.

Note precisely what is and isn't true here, because the Exclusive group policy
below is designed on top of it: `normalizeAnthropicCarrier` does **not**
guarantee the two carriers are mutually exclusive. It only reroutes *misfiled*
tokens. Two correctly-filed carriers survive `Load()` together — e.g.
`~/.cspace/secrets.env` sets `CLAUDE_CODE_OAUTH_TOKEN` while the Keychain layer
(`secrets.go:105-117`) independently fills `ANTHROPIC_API_KEY`. Exclusivity is
enforced later and separately, in `cmd_up.go:496-513` and its duplicated
preflight twin at `cmd_up.go:126-143`. Today exclusivity is emergent `cmd_up`
behavior, not a `Load()` invariant. This design makes it an invariant.

### 2. A project `env_file` silently shadows cspace credentials, unvalidated

`resume-redux/.devcontainer/docker-compose.yml` declares `env_file: ../.env`.
compose-go reads that file into the service environment, and `cmd_up.go:349`
merges it **on top of** everything `secrets.Load()` resolved.

The `cspace-resume-redux-mercury` container's baked env was measured directly:

| Key | Baked value (sha1/8) | Length |
|---|---|---|
| `GH_TOKEN` | `310607f9` | 93 |
| `GITHUB_TOKEN` | `310607f9` | 93 |
| `GITHUB_PERSONAL_ACCESS_TOKEN` | `310607f9` | 93 |

`310607f9` is byte-identical to `GH_TOKEN` in `resume-redux/.env`. The host's
`gh auth token` (`ab53e677`, 40 chars) appears nowhere in the container. The
credential that broke `git push` with a 401 came from the project's app env
file, not from a host re-authentication.

Two aggravating details:

- **The validation exists and is bypassed.** `ReconcileGitHubToken`
  (`cmd_up.go:173`) performs exactly the right liveness check — a direct
  `GET https://api.github.com/user` (`github.go:31-60`). But `effectiveGH` is
  built from `loaded` plus host shell only (`cmd_up.go:163-172`). The `env_file`
  value does not exist yet at line 173; it arrives at line 349. The preflight
  validated the *valid* host token, found nothing wrong, and left
  `ghTokenOverride` empty, so the escape hatch at line 528 never fired. The
  token that shipped was never validated by anything.
- **`propagateFamily` destroyed an explicitly-set value.** `.env` declares
  `GITHUB_PERSONAL_ACCESS_TOKEN` as a *different* 93-char token (`8dfa8d5f`).
  `propagateFamily` (`cmd_up.go:1395-1409`) picks the first non-empty by **name
  order**, so `GH_TOKEN` wins and overwrites the other two names at line 520.
  (The comment at `cmd_up.go:518-519` is about *consumer* tools — the GitHub
  family has no analogue of the Claude CLI's dual-carrier conflict prompt, so
  one value under three names is safe for consumers. It is not a claim about
  cspace's own collision warning. The clobber is real; the comment is not
  responsible for it.)

### 3. `cspace doctor` diagnoses the host, not the sandbox

`doctor` and `keychain status` re-resolve credentials on the host at invocation
time and report the source they *would* pick now. Neither reads a running
container's baked env, and neither validates against the provider. So `doctor`
reported `✓ GH_TOKEN: auto-discovered (gh auth token)` — correctly describing
host state — while the container held a dead PAT from a different source.

A diagnostic that never inspects the thing being diagnosed cannot detect the
most common failure mode, because credentials are baked into the container's
init-process environment at create time and are immutable for its life. There is
no refresh path: `grep` finds zero re-injection anywhere in the codebase.

### Why the advisory asymmetry is backwards

Anthropic credentials carry an explicit staleness advisory ("expires …; sandbox
may lose auth on long sessions"); GitHub credentials get a bare `✓`
(`probes.go:430-451`), despite an identical bake-at-create staleness model.
GitHub is arguably worse off: the host's Claude Code OAuth blob at least
refreshes host-side when `claude` runs, whereas a baked `gh` token has no
refresh path at all.

## Decisions

Settled during brainstorming on 2026-08-17. Each is load-bearing.

1. **Keychain is the canonical credential source** on macOS. The two
   `secrets.env` file layers remain readable for back-compat but are documented
   as legacy and removed from the happy path. See Decision 8 for non-darwin.
2. **Auto-discovery is kept for both Anthropic and GitHub**, preserving the
   zero-config first run. Its durability is surfaced rather than hidden.
3. **cspace's resolved credentials are the only source for the five
   cspace-owned keys.** A project's compose `env_file` and devcontainer
   `containerEnv` are ignored for those keys — unconditionally, including when
   cspace resolves nothing. Every other key in those files flows through
   untouched. This knowingly breaks three cases; see Migration.
4. **`cspace up` always prints a one-line credential summary**, escalating to a
   warning only when durability is at risk.
5. **Runway threshold:** a short-lived credential with less than 4h remaining
   escalates to a warning naming `cspace keychain init` as the durable fix.
   Overridable via `credentials.runwayWarningHours` in `.cspace.json` (integer
   hours; `0` disables). The default **must be seeded in the embedded
   `defaults.json`** rather than applied in Go, because `DeepMerge`
   (`config.go:104-153`) is a JSON round-trip in which an absent field and an
   explicit `0` are indistinguishable for a plain `int`.
6. **Extract `internal/credentials`** — resolution, policy, and reporting move
   out of both `internal/secrets` and `internal/cli`.
7. **Per-project credential scoping via Keychain naming convention** —
   `cspace-<project>-<KEY>`, preferred over the global entry. No secret material
   and no new config in the project tree. Written by a new `--project` flag on
   `cspace keychain init`.
8. **Non-darwin keeps `secrets.env` as canonical.** Keychain is a no-op off
   macOS (`keychain_other.go:10-12`, where `WriteKeychain` silently returns
   nil), and Anthropic auto-discovery does not exist there. Deprecation
   messaging for `secrets.env` is **darwin-only**; on other platforms it is the
   supported durable path and must not be nagged about. `keychain init` and
   `keychain init --project` continue to refuse off-darwin, as `init` already
   does (`cmd_keychain.go:39-43`).

Decision 7's rationale is worth recording: the dead PAT in `.env` was the right
intent through the wrong channel. Limiting a project's agents to one repo is a
legitimate goal — the host `gh` token carries `repo` scope, so today every
sandbox on the machine can reach every repo the user can. Making cspace's global
credential unconditionally win would have cemented that as the only option.
Per-project scoping makes least privilege a first-class feature instead of an
accident of `env_file` precedence.

## Design

### Package boundary

`internal/credentials` owns four verbs:

- **`Resolve(project)`** — every candidate cspace could deliver, ranked
- **`Bake(resolution, appEnv)`** — apply policy to produce the final container env
- **`Inspect(container)`** — what a running sandbox actually carries
- **`Verify(cred)`** — does this credential still work, against the provider

`internal/secrets` retains only primitives: dotenv parsing, Keychain read/write,
host discovery. It stops making policy decisions. `internal/cli` becomes a pure
consumer.

### Data model

`Resolve` returns the **full ranked candidate stack per key**, not a single
winner. This is required by the 401 fallback ladder in Error Handling: once a
baked credential is rejected, the ladder needs the losing candidates, which a
single-winner type cannot supply.

```go
type Source int

const (
    SourceEnvFlag Source = iota // --env KEY=value
    SourceProjectKeychain       // cspace-<project>-<KEY>
    SourceGlobalKeychain        // cspace-<KEY>
    SourceLegacyProjectFile     // <project>/.cspace/secrets.env
    SourceLegacyUserFile        // ~/.cspace/secrets.env
    SourceHostShell             // ambient os.Getenv
    SourceAutoDiscovered        // gh auth token / Claude Code-credentials
)

type Credential struct {
    Key       string
    Value     string
    Source    Source
    Detail    string    // e.g. "cspace-resume-redux-GH_TOKEN"
    ExpiresAt time.Time // zero = credential type carries no expiry
}

// Resolution is the ranked candidate stack for one key.
// Candidates[0] is the winner; later entries are fallback rungs.
type Resolution struct {
    Key        string
    Candidates []Credential
}
```

Durability is **derived, never stored**, and defined by whether the credential
*type* carries an expiry at all — not by how confident we feel:

| State | Meaning |
|---|---|
| `Expired` | an expiry timestamp is carried and is in the past |
| `Expiring` | an expiry timestamp is carried and falls within the runway threshold |
| `Durable` | the credential type carries **no** expiry mechanism (`sk-ant-api` key, long-lived `sk-ant-oat`, GitHub PAT). Revocation remains possible but is not predictable from the token |
| `Unknown` | the credential could not be read or its type could not be determined |

This resolves an inconsistency in revision 1, which labelled an `sk-ant-api` key
`Durable` and a GitHub PAT `Unknown` despite both carrying no expiry. Revocation
risk is covered by `Verify`, not by the durability label.

### Key groups

The asymmetry between the two families is currently tribal knowledge spread
across `normalizeAnthropicCarrier`, two carrier-dedup blocks in `cmd_up.go`, and
two `propagateFamily` calls. It becomes one declaration:

| Group | Keys | Policy |
|---|---|---|
| Anthropic | `ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN` | **Exclusive** — exactly one carrier ships, routed by `sk-ant-oat` / `sk-ant-api` prefix |
| GitHub | `GH_TOKEN`, `GITHUB_TOKEN`, `GITHUB_PERSONAL_ACCESS_TOKEN` | **Mirror** — one value under all three names |

**Mirror winner selection is by source rank, never by name order.** The
highest-ranked candidate across all three names wins and is written to all
three. Today's `propagateFamily` picks the first non-empty by *name* position,
which is the clobber mechanism in failure #2; reimplementing that would
reintroduce the bug.

### Merge order

Today app env and credentials interleave in one map that later steps may stomp.
The new flow separates them:

```
0. Extract --env values for the 5 credential keys → fed to Resolve as SourceEnvFlag
1. App env assembles as today (compose env_file, containerEnv, remaining --env, host shell)
2. Strip the 5 credential keys from that map, recording what was present (for reporting only)
3. Bake() writes cspace's resolved credentials — they cannot be shadowed
4. Apply group policy (Exclusive routing / Mirror propagation)
5. Verify() the final baked value
```

Step 0 exists because `--env` is rank 1 of *credential* precedence; folding it
into the app-env map and then stripping it at step 2 would discard it.

Step 5 is the fix for failure #2. Validation moves from "the value resolution
picked, 176 lines before the merge that replaces it" to "the value that ships."
It is the same `GET /user` call `ReconcileGitHubToken` already makes on every
boot — no new cost, correct subject.

### Precedence

Total and explicit, within the five cspace-owned keys:

| Rank | Source | Notes |
|---|---|---|
| 1 | `--env KEY=…` | explicit per-invocation intent |
| 2 | Project Keychain | `cspace-<project>-KEY` — least privilege |
| 3 | Global Keychain | `cspace-KEY` |
| 4 | Legacy `secrets.env` | project file, then user file |
| 5 | Ambient host shell | reported when it wins |
| 6 | Auto-discovery | zero-config fallback |
| — | compose `env_file`, `containerEnv` | **ignored for these 5 keys**, recorded for reporting |

### Behavior changes (all four, declared)

Revision 1 declared two; the review found four. All are intentional.

1. **`--env` now beats ambient host-shell credentials.** Closes the existing
   `2026-07-16-env-precedence-smeared-env-flag-loses-to-ambient-credentials`
   finding.
2. **Ambient host shell drops below Keychain.** Today `os.Getenv("GH_TOKEN")`
   wins outright (`cmd_up.go:523`), letting a stale export shadow a
   project-scoped token and recreate this bug class in a new place.
3. **`secrets.env` files drop below Keychain.** Today files win, and
   `secrets.go:79-81` documents that as deliberate ("project owners who want to
   lock a specific PAT … shouldn't have it shadowed by ambient Keychain
   state"). Decision 1 reverses it on macOS: Keychain is the canonical store, so
   ambient Keychain state is no longer "ambient." A user with entries in both
   silently switches credentials on upgrade — called out in Migration. On
   non-darwin the question is moot (Decision 8): Keychain is empty there, so
   files remain effectively rank 2.
4. **`containerEnv` loses its credential-override channel.** `cmd_up.go:357-363`
   deliberately runs the collision warning *before* the containerEnv merge so
   that "an intentional devcontainer.json containerEnv override" is not flagged.
   Decision 3 removes that channel for the five keys.

### Output surfaces

Three consumers, one resolved result. `credentialSource()` is deleted;
`probes.go` consumes `credentials` directly.

**`cspace up`** — one line, emitted through the overlay reporter, never raw
stderr. Today's `env_file` warning goes to stderr at `cmd_up.go:366`, after
`overlay.Start()` at line 222, and is shredded mid-render; the file's own
comment at lines 107-112 documents why that is wrong.

```
[3/8] credentials   claude ← keychain (durable) · github ← keychain:resume-redux (verified)
```

Escalating only when runway is short:

```
[3/8] credentials   claude ← auto-discovered (expires in 1h08m) · github ← keychain (verified)
      warning: Anthropic credential expires during a typical session.
               `cspace keychain init` stores a durable one.
```

**This requires new overlay surface area**, which the implementation must
budget for: `overlay.Reporter` is `{Phase(enum), Status(string), Done(),
Error(err)}` (`overlay/overlay.go:87-91`) with a fixed 8-value `Phase` enum and
no credentials phase; `Status` is a transient sub-label replaced by the next
phase; and `LineReporter.Error` is a no-op (`overlay.go:123`). Needed: a
`PhaseCredentials` enum value and a persistent note/warn channel that survives
phase advance. The runway warning must be verified to render correctly on all
three paths — overlay TTY, the buffered flush at `cmd_up.go:229-245`, and
`--no-overlay`.

When a project `env_file` set one of the five keys and lost, the line notes it
compactly without enumerating keys; `doctor` carries the detail.

**`cspace doctor`** — reports both sides, which is what makes failure #3
impossible:

```
GitHub
  ✓ host resolution   cspace-resume-redux-GH_TOKEN · verified (GET /user)
  ✗ mercury           baked 08:37 · DIVERGED from host resolution · rejected (401)
                      → `cspace down mercury && cspace up mercury` to re-bake
```

Divergence is a value comparison between `Inspect(container)` and
`Resolve(project)`. Rejection is `Verify` against the container's actual value.

**Container filter:** `Inspect` runs **only** over registry-tracked workspace
sandboxes for the project. Sidecars — compose services and the shared
`cspace-<project>-browser` — are excluded. They legitimately carry different
env, and enumerating them would report every one as DIVERGED.

**`cspace keychain status`** — renders the same `Resolve()` output it always
meant to, sharing one code path with boot.

Accepted cost: `doctor` becomes slower and depends on the substrate to enumerate
containers. When the substrate is unreachable it degrades to host-only reporting
with the container section marked unavailable, rather than erroring.

### Error handling

Governing rule: **credentials never hard-fail a boot**, with one inherited
exception — cspace still refuses to inject a credential it knows is dead
(today's already-expired check), because a sandbox that fails every SDK call is
worse than one that boots without auth and says so.

- **`Verify` returns 401** → advance to the next candidate in
  `Resolution.Candidates` and re-verify; report which rung won. This generalizes
  what `ReconcileGitHubToken` does today as a hard-coded single rung
  (`github.go:87-95`). If every candidate is rejected, warn and proceed with the
  highest-ranked one.
- **`Verify` cannot reach the provider** → `Unknown`, never `Rejected`. A flaky
  network must not be reported as a bad token and must not advance the ladder.
- **Keychain read fails for real** (locked keychain, not a miss) → hard error. A
  miss is `security` exit 44 and means "not set"; collapsing the two would make
  a locked keychain look empty.
- **Divergence** → never blocks; it is an observation about a running container.
- **Legacy `secrets.env` in use** → informational on darwin, silent elsewhere
  (Decision 8).

`Verify` must remain a pure predicate. `ReconcileGitHubToken` currently couples
verification with fallback discovery (`github.go:88`); the port splits those —
`Verify` answers only "is this credential accepted," and the ladder lives in
`Bake`.

### Project identity

Decision 7 keys Keychain entries on the project name, which is less stable than
it looks. `projectName()` (`cmd_up.go:1304-1312`, `config.go:226-229`) resolves
`$CSPACE_PROJECT` → `cfg.Project.Name` → **directory basename** → literal
`"default"`.

Two consequences must be handled:

- **`cspace keychain init --project` hard-fails** unless the project name is
  explicit — set in `.cspace.json`'s `project.name` or passed via flag. Writing
  `cspace-default-<KEY>` would create an entry silently shared by every unnamed
  project on the host, which is the opposite of least privilege.
- **A detached scoped entry falls back silently today.** If a directory is
  renamed, `cspace-<old>-GH_TOKEN` stops matching and resolution drops to the
  global (broader) credential. The `up` summary line names the winning scope
  (`keychain:resume-redux` vs `keychain`), so the downgrade is visible at every
  boot rather than invisible.

### Runtime shadowing: `extracted.env`

`lib/runtime/scripts/cspace-entrypoint.sh:44-51` (and again at 386-399) sources
`/sessions/extracted.env` at runtime, and a devcontainer
`customizations.cspace.extractCredentials` entry may target any env name —
`internal/devcontainer/model.go:65` imposes no key restrictions and
`validate.go:32-35` does not check them. An extraction naming one of the five
keys would shadow the baked credential *after* `Bake`'s guarantee applies.

In scope for this change: `devcontainer.validate` rejects an
`extractCredentials` entry whose `env` is one of the five cspace-owned keys,
with an error naming the key. Bake's invariant is worthless if a documented
config channel can overwrite it a second later.

## Migration

### What breaks, and for whom

Decision 3 is unconditional, so three cases regress. This is deliberate and
accepted; the alternative was a fallback rung or a per-project opt-out, both
declined in favor of a rule with no exceptions.

1. **A project whose `.env` is the only working source loses its credential.**
   No Keychain entry, no `secrets.env`, no host `gh` login → the sandbox
   previously got a working token via `cmd_up.go:349` and now gets none. The
   `up` summary line reports the key as unresolved, and `doctor` reports it as
   missing with the `keychain init` remedy.
2. **A project pinning a deliberately narrow token gets the broad one.** The
   narrow `.env` value is ignored; the host's `repo`-scoped token is injected
   instead. The remedy is Decision 7 — re-store the narrow token as
   `cspace-<project>-GH_TOKEN` — and the summary line's scope label makes the
   substitution visible.
3. **A project whose app code reads one of the five names gets the user's
   personal credential.** `ANTHROPIC_API_KEY` and `GITHUB_TOKEN` are not
   cspace-invented names; an app billing its own Anthropic account from `.env`
   will instead see the host user's key. There is no opt-out. Projects in this
   position must rename their app-facing variable.

Case 3 is the sharpest and must be called out in release notes, not only here.

A fourth, quieter change affects users with **both** a `secrets.env` entry and a
Keychain entry for the same key: the winner flips from file to Keychain
(behavior change 3). `cspace keychain status` shows the new winner, and the
legacy-file notice names the shadowed file.

### For the host this was designed on

1. `cspace keychain init` — store the long-lived `CLAUDE_CODE_OAUTH_TOKEN`
   currently in `resume-redux/.env`
2. `cspace keychain init --project` — store a repo-scoped GitHub PAT as
   `cspace-resume-redux-GH_TOKEN`, replacing the dead one in `.env`
3. Delete all three credential keys from `resume-redux/.env`, leaving it for app
   vars only (Resend, Gemini, Convex, …)
4. `cspace down mercury && cspace up mercury` to re-bake

Step 4 is required for any running sandbox: baked env is immutable, so no
host-side change reaches an existing container. Note that `up` cannot re-bake in
place — `container run -d --name` fails on an existing name and the adapter has
no adopt path (`adapter.go:179-279`) — so `down` first is mandatory, not
advisory.

## Testing

Precedence stops being emergent behavior of a 900-line function and becomes a
pure function over a ranked candidate stack, table-testable with no I/O.

- **Precedence matrix** — the six-rank table driven directly as test cases
- **Group policy** — Exclusive routing by token prefix; Mirror selecting by
  source rank across the three GitHub names (a name-order implementation must
  fail this test)
- **Bake invariants** — a compose `env_file` declaring all five keys shadows
  none of them; app vars in the same file pass through untouched
- **Regression for failure #2** — `.env` carrying a dead PAT plus a valid host
  token: assert cspace's value is baked and that `Verify` ran against the baked
  value. This test can only run against the new package; today's merge lives
  inside the untestable `RunE`, so it is a guard against regression, not a
  reproduction of the old bug
- **Fallback ladder** — first candidate 401s, second is accepted, and the
  reported source is the second; a network error does **not** advance the ladder
- **`Inspect`** — parse a recorded `container inspect` fixture. **Capture a real
  Apple Container 1.2 fixture before implementing**: the init-process env JSON
  path is not referenced anywhere in the repo today (the current `inspectRecord`
  parses only `status.networks`, `adapter.go:417-424`), it rests solely on a
  live measurement, and `adapter.go:4-11` documents that this CLI reshapes its
  JSON across versions. The whole `doctor` centerpiece hangs on that path
- **Divergence** — baked value ≠ resolved value produces the diverged state;
  sidecars are excluded from enumeration
- **`extractCredentials` guard** — a devcontainer naming one of the five keys is
  rejected by `devcontainer.validate`
- **Runway threshold** — the existing `timeNow` seam moves with the code;
  `runwayWarningHours: 0` disables escalation
- **Non-darwin** — `secrets.env` is not reported as deprecated; `init --project`
  refuses

No test touches the real Keychain, the real `gh`, or the network. The
package-level function-variable seams (`discoverClaudeOauthToken`,
`discoverGhAuthToken` at `secrets.go:50-53`, `timeNow` at `secrets.go:57`,
`validateGitHubToken`/`githubHTTPClient` at `github.go:25,29`) are the
established pattern; `Verify` gets an injectable transport.

## Out of scope

- **Sidecar credential env.** `Bake` governs the workspace sandbox only.
  Measurement showed this is not the hole an earlier revision claimed: sidecar
  environment is resolved per-service (`sidecars/lifecycle.go:31` passes that
  service's own `svc.Environment`), cspace injects no credentials into the
  browser sidecar, and no sidecar in the motivating project carries any cspace
  credential key. What remains is that a project may declare `env_file:` on its
  own sidecar service — standard compose behavior, and its author's choice.
- **`cspace up` against an already-running container.** The run fails on the
  existing name, but the boot has already re-`Register`ed the sandbox with a
  freshly generated control token (`cmd_up.go:795-807`), breaking `cspace send`
  and `cspace agent` against the still-running sandbox. Pre-existing, but this
  design makes `down && up` a `doctor`-recommended path, so log it as a finding
  and cross-reference it.
- Refactoring `cmd_up.go`'s ~875-line `RunE` beyond extracting credential
  handling — `2026-07-16-cspace-up-rune-monolith-implicit-phase-ordering`.
- The `-e` delivery path, which `vminitd` logs in full —
  `2026-05-01-apple-container-vminitd-logs-full-process-env-…`.
- Any in-sandbox credential refresh. Baked env stays immutable; `down` + `up`
  remains the re-bake path.
- Firewall / egress filtering, unimplemented by design.

## Findings

**Closed by this change:**

- `2026-07-16-env-precedence-smeared-env-flag-loses-to-ambient-credentials`
- the `env_file`-shadows-secrets footgun documented in `docs/env-cspace.md`

**To be logged before implementation:**

- overlay-ordering: credential warnings emitted after `overlay.Start` are
  shredded mid-render
- `ReconcileGitHubToken` validates a value the later `env_file` merge discards
- `propagateFamily` clobbers an explicitly-set `GITHUB_PERSONAL_ACCESS_TOKEN`
- `doctor` reports host resolution while claiming to describe sandbox health
- sidecar credential exposure is project-declared, not cspace-injected
  (investigated and closed; no cspace change warranted)
- `up` against a running container re-registers a fresh control token, breaking
  `send`/`agent` (out of scope above)
