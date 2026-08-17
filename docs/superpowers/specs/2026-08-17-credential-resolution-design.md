# Credential resolution: one owner, explicit precedence, container-aware reporting

- **Date:** 2026-08-17
- **Status:** approved, not yet implemented
- **Supersedes:** the credential-resolution behavior currently spread across
  `internal/secrets`, `internal/cli/cmd_up.go`, `internal/cli/cmd_keychain.go`,
  and `internal/cli/probes.go`

## Problem

cspace's credential handling works, but it is not owned by anything. Resolution
logic is reconstructed independently in at least four places, the four
reconstructions have drifted, and the drift produces wrong answers that read as
healthy ones.

Three concrete failures motivated this design. All three were reproduced on a
live host on 2026-08-17.

### 1. `cspace keychain status` reports states that cannot exist

`credentialSource()` (`cmd_keychain.go:209`) re-walks all four resolution layers
by hand — re-reading the files, re-reading the Keychain, re-running discovery.
It does not apply `normalizeAnthropicCarrier`, and it knows nothing about the
compose `env_file` merge in `cmd_up.go`.

Observed output:

```
ANTHROPIC_API_KEY        source: auto-discovered (Claude Code OAuth)
CLAUDE_CODE_OAUTH_TOKEN  source: auto-discovered (Claude Code OAuth)
```

`autoDiscover()` only ever fills `CLAUDE_CODE_OAUTH_TOKEN` (`secrets.go:190`),
and `normalizeAnthropicCarrier` guarantees the two carriers are mutually
exclusive. The first row describes a state `Load()` cannot produce. Neither row
mentions that the sandbox actually received its token from the project's `.env`.

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
  (`cmd_up.go:173`) performs exactly the right liveness check, but `effectiveGH`
  is built from `loaded` plus host shell only. The `env_file` value does not
  exist yet at line 173 — it arrives at line 349. The preflight validated the
  *valid* host token, found nothing wrong, and left `ghTokenOverride` empty, so
  the escape hatch at line 528 never fired. The token that shipped was never
  validated by anything.
- **`propagateFamily` destroyed an explicitly-set value.** `.env` declares
  `GITHUB_PERSONAL_ACCESS_TOKEN` as a *different* 93-char token (`8dfa8d5f`).
  Line 520 spreads `GH_TOKEN` across all three names, overwriting it. The
  comment at line 518 asserts the dual-write is safe because there is no
  conflict warning; the warning it relies on fires per-key, then this line
  collapses the keys.

### 3. `cspace doctor` diagnoses the host, not the sandbox

`doctor` and `keychain status` re-resolve credentials on the host at invocation
time and report the source they *would* pick now. Neither reads a running
container's baked env, and neither validates against the provider. So `doctor`
reported `✓ GH_TOKEN: auto-discovered (gh auth token)` — correctly describing
host state — while the container held a dead PAT from a different source.

A diagnostic that never inspects the thing being diagnosed cannot detect the
most common failure mode, because credentials are baked into
`configuration.initProcess.environment` at container-create time and are
immutable for the container's life. There is no refresh path: `grep` finds zero
re-injection anywhere in the codebase.

### Why the advisory asymmetry is backwards

Anthropic credentials carry an explicit staleness advisory ("expires …; sandbox
may lose auth on long sessions"). GitHub credentials get a bare `✓`, despite an
identical bake-at-create staleness model. GitHub is arguably worse off: the
host's Claude Code OAuth blob at least refreshes host-side when `claude` runs,
whereas a baked `gh` token has no refresh path at all.

## Decisions

Settled during brainstorming on 2026-08-17. Each is load-bearing for the design
below.

1. **Keychain is the canonical credential source.** The two `secrets.env` file
   layers remain readable for back-compat but are documented as legacy and
   removed from the happy path.
2. **Auto-discovery is kept for both Anthropic and GitHub**, preserving the
   zero-config first run. Its durability is surfaced rather than hidden.
3. **cspace's resolved credential wins over a project `env_file`** for the five
   cspace-owned keys. App vars in the same file are unaffected.
4. **`cspace up` always prints a one-line credential summary**, escalating to a
   warning only when durability is at risk.
5. **Runway threshold:** a short-lived credential with less than 4h remaining
   escalates to a warning naming `cspace keychain init` as the durable fix.
   The default is 4 hours, overridable via `credentials.runwayWarningHours` in
   `.cspace.json` (integer hours; `0` disables the escalation).
6. **Extract `internal/credentials`** — resolution, policy, and reporting move
   out of both `internal/secrets` and `internal/cli`.
7. **Per-project credential scoping via Keychain naming convention** —
   `cspace-<project>-<KEY>`, preferred over the global entry. No secret material
   and no new config in the project tree. Written by a new `--project` flag on
   `cspace keychain init`, which resolves the project name from `.cspace.json`'s
   `project.name` exactly as `up` does.

Decision 7 deserves its rationale recorded: the dead PAT in `.env` was the right
intent through the wrong channel. Limiting a project's agents to one repo is a
legitimate goal — the host `gh` token carries `repo` scope, so today every
sandbox on the machine can reach every repo the user can. Making cspace's global
credential unconditionally win would have cemented that as the only option.
Per-project scoping makes least privilege a first-class feature instead of an
accident of `env_file` precedence.

## Design

### Package boundary

`internal/credentials` owns four verbs:

- **`Resolve(project)`** — what cspace would deliver for this project, now, on
  this host
- **`Bake(resolved, appEnv)`** — apply policy to produce the final container env
- **`Inspect(container)`** — what a running sandbox actually carries, read from
  its baked env
- **`Verify(cred)`** — does this credential still work, against the provider

`internal/secrets` retains only primitives: dotenv parsing, Keychain read/write,
host discovery. It stops making policy decisions. `internal/cli` becomes a pure
consumer.

### Data model

`Resolve` and `Inspect` return the same type, which is what makes divergence
detection a value comparison rather than bespoke code:

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
    SourceProjectEnvFile        // compose env_file — recorded, never used
)

type Credential struct {
    Key       string    // canonical env var name
    Value     string
    Source    Source
    Detail    string    // e.g. "cspace-resume-redux-GH_TOKEN"
    ExpiresAt time.Time // zero = no known expiry
}
```

Durability is **derived, never stored**:

```go
type Durability int // Durable | Expiring | Expired | Unknown
```

so the `up` summary line, `doctor`, and the runway warning cannot disagree.
`Unknown` is the honest answer for a GitHub PAT, whose expiry is not carried in
the token — that is precisely where `Verify` earns its place.

### Key groups

The asymmetry between the two credential families is currently tribal knowledge
spread across `normalizeAnthropicCarrier`, two carrier-dedup blocks in
`cmd_up.go`, and two `propagateFamily` calls. It becomes one declaration:

| Group | Keys | Policy |
|---|---|---|
| Anthropic | `ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN` | **Exclusive** — exactly one carrier, routed by `sk-ant-oat` / `sk-ant-api` prefix |
| GitHub | `GH_TOKEN`, `GITHUB_TOKEN`, `GITHUB_PERSONAL_ACCESS_TOKEN` | **Mirror** — one value under all three names |

### Merge order

Today, app env and credentials are interleaved in one map that later steps may
stomp. The new flow separates them and applies credentials last:

```
1. App env assembles as today (compose env_file, containerEnv, --env, host shell)
2. Strip the 5 credential keys from that map, recording what was present
3. Bake() applies cspace's resolved credentials — they cannot be shadowed
4. Apply group policy (Exclusive routing / Mirror propagation)
5. Verify() the final baked value
```

Step 5 is the fix for failure #2. Validation moves from "the value resolution
picked, 176 lines before the merge that replaces it" to "the value that ships."
It is the same `gh api user` call `ReconcileGitHubToken` already makes on every
boot — no new cost, correct subject.

### Precedence

Total and explicit, within the five cspace-owned keys:

| Rank | Source | Notes |
|---|---|---|
| 1 | `--env KEY=…` | explicit per-invocation intent |
| 2 | Project Keychain | `cspace-<project>-KEY` — least privilege |
| 3 | Global Keychain | `cspace-KEY` |
| 4 | Legacy `secrets.env` | still read, reported as deprecated |
| 5 | Ambient host shell | reported when it wins |
| 6 | Auto-discovery | zero-config fallback |
| — | compose `env_file` / `containerEnv` | **ignored for these 5 keys**, recorded for reporting |

Two deliberate behavior changes:

- **`--env` now beats ambient host-shell credentials**, closing the existing
  `2026-07-16-env-precedence-smeared-env-flag-loses-to-ambient-credentials`
  finding rather than leaving it open.
- **Ambient host shell drops below Keychain.** Today `os.Getenv("GH_TOKEN")`
  wins outright (`cmd_up.go:523`), which would let a stale export shadow a
  project-scoped token and recreate this same class of bug in a new place.

### Output surfaces

Three consumers, one resolved result. `credentialSource()` is deleted.

**`cspace up`** — one line, emitted through the overlay reporter, never raw
stderr. (Today's `env_file` warning is written to stderr at `cmd_up.go:366`,
after `overlay.Start()` at line 222, and is shredded mid-render — the file's own
comment at lines 107–112 documents why that is wrong.)

```
[3/8] credentials   claude ← keychain (durable) · github ← keychain:resume-redux (verified)
```

Escalating only when runway is short:

```
[3/8] credentials   claude ← auto-discovered (expires in 1h08m) · github ← keychain (verified)
      warning: Anthropic credential expires during a typical session.
               `cspace keychain init` stores a durable one.
```

When a project `env_file` set one of the five keys and lost, the line notes it
compactly without enumerating keys; `doctor` carries the detail.

**`cspace doctor`** — reports both sides, which is what makes failure #3
impossible:

```
GitHub
  ✓ host resolution   cspace-resume-redux-GH_TOKEN · verified (gh api user)
  ✗ mercury           baked 08:37 · DIVERGED from host resolution · rejected (401)
                      → `cspace down mercury && cspace up mercury` to re-bake
```

Divergence is a value comparison between `Inspect(container)` and
`Resolve(project)`. Rejection is `Verify` against the container's actual value.

**`cspace keychain status`** — renders the same `Resolve()` output it always
meant to, sharing one code path with boot.

Accepted cost: `doctor` becomes slower and depends on the substrate to enumerate
a project's containers. When the substrate is unreachable it degrades to
host-only reporting with the container section marked unavailable, rather than
erroring.

### Error handling

Governing rule: **credentials never hard-fail a boot**, with one inherited
exception — cspace still refuses to inject a credential it knows is dead
(today's already-expired check), because a sandbox that fails every SDK call is
worse than one that boots without auth and says so.

- **`Verify` returns 401** → fall down the precedence ladder to the next
  available source and report which won. This generalizes what
  `ReconcileGitHubToken` does today as a special case. If every source is
  rejected, warn and proceed.
- **`Verify` cannot reach the provider** → `Unknown`, never `Rejected`. A flaky
  network must not be reported as a bad token and must not trigger the fallback
  ladder.
- **Keychain read fails for real** (locked keychain, not a miss) → hard error.
  A miss is `security` exit 44 and means "not set"; collapsing the two would
  make a locked keychain look empty.
- **Divergence** → never blocks; it is an observation about an already-running
  container.
- **Legacy `secrets.env` in use** → informational, never fatal.

### Testing

Precedence stops being emergent behavior of a 900-line function and becomes a
pure function over a resolved set, table-testable with no I/O.

- **Precedence matrix** — the six-row table driven directly as test cases
- **Group policy** — Exclusive routing by token prefix; Mirror across the three
  GitHub names
- **Bake invariants** — a compose `env_file` declaring all five keys shadows
  none of them; app vars in the same file pass through untouched
- **Regression for failure #2** — `.env` carrying a dead PAT plus a valid host
  token: assert cspace's value is baked, and that `Verify` ran against the baked
  value rather than the resolved one. This test fails against today's code
- **`Inspect`** — parse a recorded `container inspect` fixture, following the
  existing 1.1.x/1.2 fixture pattern in `adapter_test.go`
- **Divergence** — baked value ≠ resolved value produces the diverged state
- **Runway threshold** — the existing `timeNow` seam moves with the code

No test touches the real Keychain, the real `gh`, or the network. The
package-level function-variable seams (`discoverClaudeOauthToken`,
`discoverGhAuthToken`) are the established pattern; `Verify` gets an injectable
transport.

## Migration

For the host this was designed on:

1. `cspace keychain init` — store the long-lived `CLAUDE_CODE_OAUTH_TOKEN`
   currently in `resume-redux/.env`
2. `cspace keychain init --project` — store a repo-scoped GitHub PAT as
   `cspace-resume-redux-GH_TOKEN`, replacing the dead one in `.env`
3. Delete all three credential keys from `resume-redux/.env`, leaving it for app
   vars only (Resend, Gemini, Convex, …)
4. `cspace down mercury && cspace up mercury` to re-bake

Step 4 is required for any running sandbox: baked env is immutable, so no
host-side change reaches an existing container.

Projects that keep credentials in `.env` are not broken by this change — those
values are ignored for the five cspace keys and reported, and every other key in
the file continues to flow through untouched.

## Out of scope

- Refactoring `cmd_up.go`'s ~875-line `RunE` beyond extracting credential
  handling. Tracked separately as
  `2026-07-16-cspace-up-rune-monolith-implicit-phase-ordering`.
- The `-e` flag delivery path, which Apple Container's `vminitd` logs in full.
  Tracked as `2026-05-01-apple-container-vminitd-logs-full-process-env-…`.
- Any in-sandbox credential refresh. Baked env stays immutable for a container's
  life; `down` + `up` remains the re-bake path.
- Firewall / egress filtering, which remains unimplemented by design.

## Findings this closes

- `2026-07-16-env-precedence-smeared-env-flag-loses-to-ambient-credentials`
- The `env_file`-shadows-secrets footgun documented in `docs/env-cspace.md`
- New findings to be logged for the four defects above: the overlay-ordering
  garble, the bypassed `ReconcileGitHubToken` validation, the `propagateFamily`
  clobber, and host-only `doctor` reporting.
