# General Agent Supervisor — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the supervisor cspace's configurable general agent (role file, model, project settings, honest liveness, interrupt/status) and delete the specialized-agents residue.

**Architecture:** Spec: `docs/superpowers/specs/2026-07-19-general-agent-supervisor-design.md` — read it first; its Decisions govern. Go CLI changes ride the established patterns (`cmd_browser.go` dual-context group, `cmd_send.go` resolveEntry, seam vars); supervisor changes are Bun/TS with new `bun test` coverage; entrypoint/loop are shell (shellcheck-gated via `make lint`).

**Tech Stack:** Go (Cobra), Bun/TypeScript (`@anthropic-ai/claude-agent-sdk` — pinned version's real option names MUST be read from `lib/agent-supervisor-bun/node_modules/@anthropic-ai/claude-agent-sdk/sdk.d.ts`, never guessed), shell.

## Global Constraints

- TDD per task: failing test → RED evidence → implement → GREEN → commit.
- Every `go test ./internal/cli` invocation MUST use `-skip 'TestCspaceLifecycle'`. Never invoke the `container` CLI or touch live containers in tests. Do not run `go test ./...` (adapter integration tests create containers); use targeted package lists.
- Bun tests: `cd lib/agent-supervisor-bun && bun test` (bun is installed on this host). Tests must not call the real SDK — fake `runClaude`'s seam.
- Supervisor source changes require `make sync-embedded` to reach the embedded FS — automatic via `make build`/`make test`.
- Ports/paths from existing constants; DNS suffix via `daemonDNSDomain`; no literals.
- Commit messages: short imperative; findings resolved in Task 7 carry their `(cs-finding:<slug>)` refs.

---

### Task 1: Removal cut — dead config, dead MCP registration, spec archive, CLAUDE.md

**Files:**
- Modify: `internal/config/config.go` (delete `Advisors` field + `AdvisorConfig`; delete `Verify`, `Agent`, `Claude`, `PostSetup` fields + their structs + `CoordinatorModel`; KEEP `Services` and `Container` — they are consumed/deprecation-warned in cmd_up.go:407-416)
- Modify: `lib/defaults.json` (delete `advisors`, `agent`, `verify`, `post_setup`, `claude` blocks)
- Delete: `.mcp.json` (registers nonexistent `cspace context-server`)
- Move: `docs/superpowers/specs/2026-04-13-context-mcp-server-design.md` and `2026-04-18-advisor-agents-design.md` → `docs/superpowers/specs/archive/` (git mv), each gaining a top header line: `> ARCHIVED — describes a design that was never shipped in this form; see CLAUDE.md for current architecture.`
- Modify: `CLAUDE.md` (replace the defaults.json vestigial-keys sentence with: cspace ships primitives — `up`/`send`/`down`/`browser`, the supervisor; orchestration patterns live in project-side skills such as resume-redux's `delegate-to-containers`)
- Test: `internal/config/config_test.go`

**Interfaces:** Produces a `config.Config` without the dead fields. Task 4 later ADDS a new consumed `Agent struct{ Model string }` — do not preserve the old one for it.

- [ ] **Step 1: failing test** — add `TestLoadIgnoresUnknownKeys`: write a temp `.cspace.json` containing `{"advisors":{"x":{}},"claude":{"model":"m"},"verify":{"all":"a"},"agent":{"issue_label":"l"},"post_setup":"s"}` plus a valid `project.name`; `config.Load` must succeed and the loaded config must equal the same file without those keys. (RED now only if removal is done first — so write the test, delete the fields, and confirm the test passes AND pre-existing config tests still pass; RED evidence here is the compile failure of any code still referencing deleted fields.)
- [ ] **Step 2:** delete fields/structs/blocks; fix compile fallout (grep `cfg.Verify\|cfg.Agent\|cfg.Claude\|cfg.PostSetup\|Advisors` — expected fallout: none outside config.go per prior survey; verify).
- [ ] **Step 3:** `.mcp.json` delete, spec moves + headers, CLAUDE.md edit.
- [ ] **Step 4:** `go test ./internal/config ./internal/cli -skip 'TestCspaceLifecycle'`, `make vet`, `make lint` green.
- [ ] **Step 5:** Commit: `Remove specialized-agent residue: dead config, dead MCP registration, archived specs`

### Task 2: Rename internal/orchestrator → internal/sidecars

**Files:** `git mv internal/orchestrator internal/sidecars`; package clause `orchestrator` → `sidecars`; update all importers (grep `internal/orchestrator`); tests move with it.

- [ ] Mechanical rename, zero behavior change. `go build ./...` + targeted tests + vet + lint green. Commit: `Rename internal/orchestrator to internal/sidecars (it manages compose sidecars, not agents)`

### Task 3: Supervisor liveness + loop script

**Files:**
- Modify: `lib/agent-supervisor-bun/src/main.ts`
- Create: `lib/agent-supervisor-bun/src/event-log.ts` (extract `logEvent` + rotation + `resumeSessionId` so they're testable), `lib/agent-supervisor-bun/src/event-log.test.ts`, `lib/agent-supervisor-bun/src/prompt-stream.test.ts`
- Modify: `lib/runtime/scripts/cspace-supervisor-loop.sh` (remove `137` from the clean-exit list; comment: OOM SIGKILL is a crash and must respawn — cs-finding 2026-07-16-supervisor-silent-death-modes-and-fail-open-auth)

**Behavior contract (spec §3):**
1. `runClaude` rejection → log `sdk-error` → if a resume id was in use, retry ONCE with `resume` unset (log `resume-failed`); a second rejection (or rejection with no resume) → `process.exit(1)`.
2. Empty `CSPACE_CONTROL_TOKEN` → log fatal to stderr and `process.exit(1)` BEFORE `Bun.serve`.
3. `logEvent` rotates: when `events.ndjson` ≥ 10 MiB (`statSync` best-effort), rename to `events.ndjson.1` (clobbering any prior `.1`) before appending. `resumeSessionId` reads only the current file.

- [ ] **Step 1: failing tests** — `event-log.test.ts`: rotation at threshold (temp dir, write >10MiB? use an injectable `maxBytes` param defaulting 10 MiB and test with 1 KiB), `.1` clobbered, append lands in fresh file; `resumeSessionId` returns last init id, skips malformed lines, returns undefined post-rotation-freshness. `prompt-stream.test.ts`: push-then-iterate, iterate-then-push (waiter), close drains. Run `bun test` → RED (module missing).
- [ ] **Step 2:** implement `event-log.ts`; rewire main.ts to it; add the retry-once-fresh + exit paths and the token gate; edit loop script.
- [ ] **Step 3:** `bun test` GREEN; `make lint` (shellcheck) green; `make vet`; targeted Go tests still green (`sync-embedded` runs via make).
- [ ] **Step 4:** Commit: `Harden supervisor liveness: exit on stream death, fresh-session retry, fail-closed token, log rotation, OOM respawn`

### Task 4: Configuration surface (role, model, project settings)

**Files:**
- Modify: `internal/cli/cmd_up.go` (flags `--role <host-path>`, `--model <name>`; before container start: if `--role`, read the file and write its content to `<sessionsDir>/agent-role.md` — the host-side sessions dir cspace already creates; if `--model` or `cfg.Agent.Model`, set `env["CSPACE_AGENT_MODEL"]`)
- Modify: `internal/config/config.go` (NEW `Agent struct{ Model string \`json:"model,omitempty"\` }` + `agent` block; defaults.json gains `"agent": {"model": ""}`)
- Modify: `lib/agent-supervisor-bun/src/main.ts` + `claude-runner.ts`
- Create: `lib/agent-supervisor-bun/src/role.ts` + `role.test.ts`
- Test: `internal/cli/cmd_up_test.go`

**Behavior contract (spec §2):**
- Role resolution (in `role.ts`, injectable paths for tests): `/sessions/agent-role.md` if present → else `/workspace/.cspace/agent.md` if present → else none. Content is passed to the SDK's append-style system-prompt option — the implementer MUST read `sdk.d.ts` for the pinned version and use the append variant (contract: Claude Code's own system prompt is never replaced). Same for project-settings loading (contract: `/workspace/CLAUDE.md` reaches the agent; use the `settingSources`-style option the pinned SDK exposes).
- `CSPACE_AGENT_MODEL` non-empty → SDK `model` option.
- Nothing configured → byte-identical `query()` options to today (assert by unit test on the options-builder if extracted, else by code review).

- [ ] **Step 1: failing tests** — Go: `TestUpRoleFlagWritesSessionRoleFile` (temp sessions dir seam — follow how cmd_up resolves the sessions dir; if inline, extract a small helper `writeAgentRole(sessionsDir, hostPath) error` and test THAT), `TestAgentModelEnvFromConfigAndFlag` (flag overrides config; both empty → env key absent). Bun: `role.test.ts` resolution order incl. both-present and neither.
- [ ] **Step 2:** implement Go + Bun sides (reading sdk.d.ts first; record the exact option names used in the task report).
- [ ] **Step 3:** all suites green (Go targeted + bun test + vet + lint). Commit: `Add agent config surface: role file convention, --role/--model, project settings loading`

### Task 5: Steering — /interrupt, /status, cspace agent CLI

**Files:**
- Modify: `lib/agent-supervisor-bun/src/main.ts`, `claude-runner.ts` (expose the SDK query handle: `runClaude` gains an `onQuery?: (q) => void` callback invoked with the live query object before iteration; main.ts stores it), `prompt-stream.ts` (add `depth(): number`)
- Create: `internal/cli/cmd_agent.go`, `internal/cli/cmd_agent_test.go`; register `newAgentCmd()` in root.go
- Test: bun tests for status-state derivation (pure function `deriveState(lastEvent): 'working'|'idle'` in a new `status.ts`)

**Behavior contract (spec §4):**
- `POST /interrupt` (token-authed like /send): calls `query.interrupt()` if a handle exists; 200 `{ok:true}`; 409 `{ok:false,error:"no active task"}` when no handle.
- `GET /status` (token-authed): `{ok:true, session, state, lastEventTs, lastEventType, queueDepth}` — main.ts tracks lastEventTs/type in the event sink; `state` from `deriveState` (`working` unless the last event is a `result`-type/system idle marker — implementer reads the SDK event taxonomy in sdk.d.ts and documents the chosen markers); `queueDepth` = `prompts.depth()`.
- `cspace agent status|interrupt <sandbox>`: dual-context exactly like `cspace browser` — host: registry lookup (Path) for control URL+token; in-sandbox: `resolveEntry` via `CSPACE_REGISTRY_URL` (mirror cmd_send.go). Status prints one line per field; interrupt prints the server response; non-2xx → non-zero exit with server text (reuse the JSON-error pretty-print pattern from cmd_browser.go's `restartErrorText`).

- [ ] **Step 1: failing tests** — bun: `deriveState` cases; Go: httptest fake control server serving /status + /interrupt with token assertion, both contexts (t.Setenv sandbox vars), non-2xx path. RED → implement → GREEN.
- [ ] **Step 2:** all suites + vet + lint green. Commit: `Add agent steering: /interrupt + /status routes, cspace agent CLI`

### Task 6: Boot-stall passenger — bounded plugin installs, phase-aware health wait

**Files:**
- Modify: `lib/runtime/scripts/cspace-install-plugins.sh` (wrap each `claude plugins marketplace add` / `claude plugins install` in `timeout 120`, one retry, then warn-and-continue — plugins are enhancement, not boot-critical)
- Modify: `internal/cli/cmd_up.go` (the supervisor /health wait: also read `<sessionsDir>/cspace-init.status` from the HOST side each poll; while the phase value CHANGES between polls, reset the patience budget; on final timeout, error message names the last-seen phase: `sandbox stuck in '<phase>' phase after <d>` — extract the wait into `waitSupervisorHealth(ctx, healthURL, statusPath string, budget time.Duration) error` for testability)
- Test: `internal/cli/cmd_up_test.go` (temp status file whose content a goroutine advances → wait outlives a single budget; static phase → errors naming it; healthy URL → nil), shellcheck via `make lint`

- [ ] TDD as above → green → Commit: `Bound plugin installs and make the boot health wait phase-aware (cs-finding:2026-07-19-plugins-marketplace-add-can-stall-boot-past-health-wait)`

### Task 7: Docs + findings

**Files:** `CLAUDE.md` (supervisor section → general agent: role convention, `--role/--model`, `cspace agent status|interrupt`, liveness semantics incl. 137-respawns); resolve findings `2026-07-16-supervisor-silent-death-modes-and-fail-open-auth` (status → resolved, Updates entry naming all four fixes) and `2026-07-19-plugins-marketplace-add-can-stall-boot-past-health-wait` (if Task 6 landed); memory index already updated.

- [ ] Edits → `make lint` → Commit: `Docs: supervisor is the general agent (cs-finding:2026-07-16-supervisor-silent-death-modes-and-fail-open-auth)`

---

## Verification (post-merge, release rc.39)

Release + tap patch + brew + `cspace image build` (supervisor, loop, entrypoint, plugins script all ship in the image). Then a disposable sandbox: role file honored (send "what is your role" → answer reflects `.cspace/agent.md`), `cspace agent status` shows idle/working truthfully, `cspace agent interrupt` stops a long task mid-flight, `kill -9` of the supervisor process respawns it and the conversation resumes, and a boot with plugins network-stalled (if reproducible) reports the phase instead of a blind timeout.

## Self-review notes

- Task 1 deletes old `Agent`; Task 4 introduces the new one — sequenced, no collision. Task 2's rename precedes Tasks 4-6 so their diffs use `internal/sidecars` paths if touched (they don't import it; ordering is for repo-wide grep cleanliness).
- SDK option names are deliberately resolved from `sdk.d.ts` at implementation with contracts pinned in the spec — implementers must record chosen names in task reports.
- All new HTTP routes reuse the existing token gate; no new auth model.
