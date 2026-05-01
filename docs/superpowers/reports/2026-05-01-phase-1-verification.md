# Phase 1 Verification Report

**Date:** 2026-05-01
**Spec:** [docs/superpowers/specs/2026-04-30-sandbox-architecture-design.md](../specs/2026-04-30-sandbox-architecture-design.md)
**Plan:** [docs/superpowers/plans/2026-05-01-phase-1-canonical-cspace2.md](../plans/2026-05-01-phase-1-canonical-cspace2.md)
**Branch:** `phase-0-prototype`
**Commits since plan landed (`c757bad..HEAD`):** 24 (the rename, the absorb-spike-design tasks, the spikes that informed them, plus the integration test and this report)

## Executive summary

Phase 1 succeeded. All 13 planned tasks landed; the cspace2-* surface is the canonical end-to-end UX described in the spec (one `up`, one `send`, one `down`; auto-provisioned per-sandbox clone; optional `--browser` sidecar; substrate health-checked preflight; idle-shutdown registry-daemon; macOS Keychain in the secret resolver). A live two-sandbox demo authenticated against real Claude (`apiKeySource: "ANTHROPIC_API_KEY"`), produced real assistant responses on host->A and A->B sends, and the lifecycle integration test passes in 2.29s. The browser sidecar drove a real DOM round-trip ("Example Domain" returned via `mcp__playwright__browser_navigate`). **Recommend: one short pre-cutover loop to address the open vminitd env-leak finding and the cspace2-*->cspace-* rename mechanics; do not cut over today.** Specifics in [Recommendation](#recommendation).

## Tasks delivered

| #  | Task                                                | Commit    | Status |
| -- | --------------------------------------------------- | --------- | ------ |
| 1  | `internal/sandboxmode` package                      | `e5694b3` | done   |
| 2  | Skip-list hack -> `sandboxmode`                     | `7a74f16` | done   |
| 3  | Rename `prototype-*` -> `cspace2-*`                 | `4323b48` | done   |
| 4  | gh CLI in image (verified end-to-end)               | `0807f7a` | done (P0 ext, re-verified in P1 demo) |
| 5  | Registry-daemon idle shutdown + status/stop         | `75884f4` | done   |
| 6  | macOS Keychain layer in secrets resolver            | `778a3be` | done   |
| 7  | `cspace init-keychain` prompt                       | `271e23c` | done   |
| 8  | Substrate `HealthCheck` preflight                   | `a154ec1` | done   |
| 9  | Bake dev toolchain into image                       | `d9b49d4` | done   |
| 10 | Workspace-clone auto-provision                      | `17b3b62` | done   |
| 11 | Browser sidecar lifecycle in `cspace2-up --browser` | `d9314cd` | done   |
| 12 | Lifecycle integration test                          | `1fc7d08` | done   |
| 13 | This report                                         | (this commit) | done (terminal) |

(SHAs from `git log --oneline c757bad..HEAD`; full list in `/tmp/p1-commits.log`.)

## Capabilities verified

### 1. Sandbox lifecycle (`cspace2-up` / `cspace2-send` / `cspace2-down`)

- `/tmp/p1-up-A.log`:
  ```
  workspace clone: /Users/elliott/.cspace/clones/cspace/A (branch cspace/A)
  sandbox A up: control http://192.168.64.97:6201  ip 192.168.64.97  token b1432923...
  ```
- `/tmp/p1-up-B.log`:
  ```
  workspace clone: /Users/elliott/.cspace/clones/cspace/B (branch cspace/B)
  sandbox B up: control http://192.168.64.98:6201  ip 192.168.64.98  token cf4df48e...
  ```
- `/tmp/p1-host-A.log`:
  ```
  {"ok":true}
  ```
  (Host -> A user-turn injection. Compare to P0 same-shape evidence.)
- `cspace2-down A` and `cspace2-down B` both returned `sandbox <X> down`. After teardown, `container ls --all | grep cspace2` returned empty; `~/.cspace/sandbox-registry.json` is `{}` (clean).
- `TestCspace2Lifecycle` PASS in **2.29s** (`/tmp/p1-integration-test.log`):
  ```
  === RUN   TestCspace2Lifecycle
  --- PASS: TestCspace2Lifecycle (2.29s)
  PASS
  ok      github.com/elliottregan/cspace/internal/cli     2.537s
  ```

### 2. Cross-sandbox messaging via in-sandbox `cspace cspace2-send`

- `/tmp/p1-A-B.log`: `{"ok":true}` (output of `container exec cspace2-cspace-A cspace cspace2-send B "A -> B: hi"`).
- `/tmp/p1-A-events.ndjson` line 2 — A's outbound user-turn from the host send:
  ```
  {"ts":"2026-05-01T06:53:36.111Z","session":"primary","kind":"user-turn","data":{"source":"control-port","text":"host -> A: hello"}}
  ```
- `/tmp/p1-B-events.ndjson` line 2 — landed inside B with the expected text from A:
  ```
  {"ts":"2026-05-01T06:53:40.504Z","session":"primary","kind":"user-turn","data":{"source":"control-port","text":"A -> B: hi"}}
  ```
- Both sandboxes returned **real Claude responses** on the same path (`apiKeySource: "ANTHROPIC_API_KEY"`, `model: "claude-sonnet-4-6"`):
  - A: `"Hello! How can I help you today?"` (`/tmp/p1-A-events.ndjson` line 5; `is_error: false`, `duration_ms: 5739`).
  - B: `"Hi! How can I help you today?"` (`/tmp/p1-B-events.ndjson` line 5; `is_error: false`, `duration_ms: 3244`).
- Mechanism (unchanged from P0, now under the canonical `cspace2-*` name): in-sandbox `cspace` reads `CSPACE_REGISTRY_URL=http://192.168.64.1:6280` from env, GETs `/lookup/cspace/B` from the host registry-daemon, POSTs to B's direct `192.168.64.98:6201/send` over the Apple Container bridge.

### 3. Auto-provisioned workspace clone

- `~/.cspace/clones/cspace/A` was created by `cspace2-up A` (visible in stdout: `workspace clone: /Users/elliott/.cspace/clones/cspace/A (branch cspace/A)`). After `cspace2-down A`, `ls -la ~/.cspace/clones/cspace/A` showed the full repo intact (`.git/` is a directory, not a worktree pointer file; checked-out branch `cspace/A`; HEAD at `1fc7d08`). Design intent: clones survive teardown so work is not lost.
- `/tmp/p1-clone-state.log` (run inside the live A sandbox before teardown):
  ```
  git is dir
  cspace/A
  1fc7d08 Add cspace2 lifecycle integration test
  ```
- After this verification step the clones were removed (`rm -rf ~/.cspace/clones/cspace/A ~/.cspace/clones/cspace/B`) so the report doesn't claim leftover state.

### 4. Apple Container `HealthCheck` preflight

- `cspace2-up` calls `substrate.HealthCheck` before any `container run`; if `container system status` reports stopped, the user gets a clear actionable error instead of a confusing late-stage failure. Verified via the PATH-shim test in Task 8 (commit `a154ec1`); not exercised in the live demo since the apiserver was running, which is the intended path.

### 5. Registry-daemon idle shutdown + escape-hatch commands

- `cspace registry-daemon status` and `cspace registry-daemon stop` shipped in commit `75884f4`.
- Idle shutdown verified at `CSPACE_REGISTRY_DAEMON_IDLE=10s` during Task 5 implementation: daemon exits at 10s + tick when the sandbox-registry transitions to empty.
- During the demo, the daemon spun up on first `cspace2-up` and was reachable from both sandboxes (confirmed by the cross-sandbox message landing in B). Post-teardown, the daemon idle-shuts on its default timer.

### 6. macOS Keychain integration

- `secrets.ReadKeychain` + `WriteKeychain` round-trip test passes against the real Keychain (commit `778a3be`).
- Resolver order verified at four layers: Keychain -> `~/.cspace/secrets.env` -> project `.cspace/secrets.env` -> `keychain:<svc>` deref of values stored elsewhere.
- `cspace init-keychain` prompts in skip-everything mode (no-op exit) and write-then-delete mode (commit `271e23c`); both verified by the maintainer during Task 7 implementation.

### 7. `gh` CLI inside cspace2 image

- Built into the image at `/usr/local/bin/gh` (Dockerfile.prototype, commit `0807f7a`).
- Live verified from within the running A sandbox (`/tmp/p1-toolchain.log`): `gh version 2.92.0 (2026-04-28)`.
- The full `GH_TOKEN` flow via `.cspace/secrets.env` was end-to-end verified in the P0 extension and is unchanged in cspace2.

### 8. Dev toolchain pre-baked into image

- `cspace2:latest` size: **566,039,282 bytes (~540 MiB)** uncompressed (`container image inspect cspace2:latest`).
- Tools verified live from inside the running A sandbox (`/tmp/p1-toolchain.log`):

  | Tool      | Version                              |
  | --------- | ------------------------------------ |
  | go        | 1.23.4 base, self-bumps to 1.25.8 via toolchain |
  | make      | GNU Make 4.3                         |
  | python3   | 3.11.2                               |
  | gcc       | Debian 12.2.0                        |
  | ripgrep   | 13.0.0                               |
  | jq        | 1.6                                  |
  | pnpm      | 9.15.9                               |
  | node      | 20.20.2                              |
  | claude    | 2.1.126                              |
  | gh        | 2.92.0                               |
  | bun       | 1.3.13                               |

- Agent task baseline measurement (Task 9 finding, now resolved): a "go test" task dropped from ~23 turns (P0, agent had to install Go) to **2 turns** with toolchain baked in.

### 9. Browser sidecar (`--browser` flag)

- `/tmp/p1-browser-up.log`:
  ```
  workspace clone: /Users/elliott/.cspace/clones/cspace/bp1 (branch cspace/bp1)
  browser sidecar: cspace2-cspace-bp1-browser (cdp http://192.168.64.100:9222)
  sandbox bp1 up: control http://192.168.64.101:6201  ip 192.168.64.101  token 5dc71ad8...
  ```
- `/tmp/p1-browser-events.ndjson` contains `mcp__playwright__browser_navigate` (7 occurrences across tool_use/tool_result + assistant rendering) and `Example Domain` (3 occurrences in tool_result snapshots and the assistant's final answer).
- The agent's final assistant message included `Example Domain` as the page title, returned via the Playwright MCP -> CDP bridge to the sidecar's headless Chromium. Real DOM round-trip end-to-end.
- `cspace2-down bp1` stopped both sandbox and sidecar; post-teardown `container ls --all | grep cspace2` returned empty and the registry's `browser_container` field was cleared.

### 10. Sandboxmode-driven config skip

- Host-side cspace loads config normally (`.cspace.json` discovered and merged).
- In-sandbox cspace skips config load via `sandboxmode.IsInSandbox()` (commits `e5694b3` + `7a74f16`).
- The hardcoded prototype-* skip-list is gone — any future `cspace2-*` -> `cspace-*` rename does not require touching the skip logic.

### 11. `CLAUDE_CODE_OAUTH_TOKEN` -> `ANTHROPIC_API_KEY` alias

- Carried over from P0 extension (commit `b454261`).
- Verified during the live demo: every `system/init` event in `/tmp/p1-A-events.ndjson` and `/tmp/p1-B-events.ndjson` shows `apiKeySource: "ANTHROPIC_API_KEY"` despite the host's `.cspace/secrets.env` storing the credential as `CLAUDE_CODE_OAUTH_TOKEN`.

### 12. DNS injection at substrate layer

- `RunSpec.DNS` field on the substrate API, default `["1.1.1.1","8.8.8.8"]` (commit `b454261`, formalized in Task 8).
- The browser sidecar gets explicit `--dns 1.1.1.1 --dns 8.8.8.8` via the same plumbing (commit `d9314cd`).
- All sandboxes resolve external DNS without manual `/etc/resolv.conf` editing — the demo's `apiKeySource: "ANTHROPIC_API_KEY"` outcome implies anthropic.com resolved.

## Open findings

From `.cspace/context/findings/`:

1. **`2026-05-01-apple-container-vminitd-logs-full-process-env-leaking-e-secr` — open.** Apple Container's `vminitd` logs the full process env, exposing values passed via `-e` (including `ANTHROPIC_API_KEY`) through `container logs --boot`. Per maintainer direction, deferred from P1. Mitigation paths for a future task: tmpfs delivery, stdin pipe, or unix socket. Same risk profile applies to all secrets in cspace2 today.

All other tracked findings moved to `status: resolved` during P1 work:

- `2026-05-01-apple-container-default-dns-is-broken-...` — resolved by Task 8 (DNS injection).
- `2026-05-01-browser-sidecar-pattern-works-under-apple-container-...` — resolved by spike + Task 11.
- `2026-05-01-claude-auth-in-sandboxes-anthropic-api-key-accepts-oauth-tok` — resolved by Task 8 + alias logic.
- `2026-05-01-claude-tool-surface-read-bash-write-...` — resolved by spike (commit `c3ad5e2`).
- `2026-05-01-multi-turn-persistent-claude-session-...` — resolved by spike (commit `cf73e52`).
- `2026-05-01-per-sandbox-git-clone-bind-mounted-as-workspace-...` — resolved by Task 10.
- `2026-05-01-persistent-claude-session-survives-idle-gaps-...` — resolved by spike (commit `bc014cc`).
- `2026-05-01-sandbox-image-lacks-dev-toolchain-go-etc-...` — resolved by Task 9.

## P1 risks for cutover

Things that may bite a `cspace2-*` -> `cspace *` rename or the work that follows:

- **Old `prototype-*` runs still leave entries in `~/.cspace/sandbox-registry.json` with the old `cspace-` prefix.** The new `cspace2-down` does not touch them. On this machine the registry is currently empty, so it is not a current bug — but anyone with leftover P0 state needs manual cleanup. Suggested follow-up: `cspace registry prune`.
- **The 97 MB Bun supervisor binary tracked in git.** GitHub flags individual files >50 MB. Acceptable for the prototype branch; a real cutover should consider git-lfs, build-on-demand, or a signed release artifact.
- **Browser sidecar cold-start cost.** First time a sidecar spins up for a sandbox, Chromium plus the in-image install adds ~10–15s. Sidecars are torn down on `cspace2-down`, so each session pays this once. A custom lean browser image (Chromium + socat baked in, no full Playwright deps surface) would shave it; not blocking.
- **macOS Keychain reads prompt on first cspace binary execution.** The user has to "Always Allow." Re-signed builds re-prompt. Document in the cutover release notes.
- **The `vminitd` env-leak finding is still open.** ANY secret transiting `-e` is logged. P2 should resolve before the `cspace2-*` -> `cspace *` cutover ships beyond single-user dev.
- **Image size grew from 293 MiB (P0) -> 540 MiB (cspace2:latest)** as a cost of the baked toolchain. The agent-turn savings (Task 9: 23 -> 2 turns on a real task) are large enough that the trade is correct, but pull time on cold registries (P5) needs to be revisited if/when we ship multi-arch.
- **Apple Container CLI surface drift remains a risk.** Same shape as P0 risk #3; not amplified by P1 but not narrowed either.

## Recommendation

**One short pre-cutover loop, do not rename today.** Phase 1 met its goal: the cspace2-* surface delivers a one-command, real-Claude, browser-capable sandbox with a clean control plane and a tested lifecycle. The architecture spec has now survived the test of "actually use it for a real task" (Task 9's 2-turn `go test` baseline) and the test of "drive a real browser through it" (Task 11). What gates the rename to top-level `cspace`:

1. **Resolve the `vminitd` env-leak finding,** or take an explicit decision to ship single-user-dev cutover with the leak documented in the release notes. This is a real privacy/security regression vs. today's `cspace`, which doesn't put secrets through `-e`.
2. **Add `cspace registry prune`** so users with legacy `prototype-*` registry entries don't have to hand-edit JSON during the rename.
3. **Decide the supervisor binary distribution story** (git-lfs vs build-on-demand vs released artifact) before the rename pollutes a wider audience's clone times.

Items (2) and (3) are 1–2-day asks; item (1) is the only gate that needs design work. Deferring the rename until those land keeps the cutover painless. If the user wants to ship the rename anyway and accept those as immediate post-cutover follow-ups, that is a defensible call — the lifecycle works.

## Artifacts

- **Spec:** [docs/superpowers/specs/2026-04-30-sandbox-architecture-design.md](../specs/2026-04-30-sandbox-architecture-design.md)
- **P0 prototype plan:** [docs/superpowers/plans/2026-04-30-phase-0-prototype.md](../plans/2026-04-30-phase-0-prototype.md)
- **P0 prototype report:** [docs/superpowers/reports/2026-05-01-phase-0-prototype-report.md](./2026-05-01-phase-0-prototype-report.md)
- **P1 plan:** [docs/superpowers/plans/2026-05-01-phase-1-canonical-cspace2.md](../plans/2026-05-01-phase-1-canonical-cspace2.md)
- **P1 verification report:** this document
- **Spike scripts (P1):** `scripts/spikes/2026-05-01-*.sh|.py`
- **Branch:** `phase-0-prototype`
- **Demo evidence files (host `/tmp`):** `p1-up-A.log`, `p1-up-B.log`, `p1-host-A.log`, `p1-A-B.log`, `p1-A-events.ndjson`, `p1-B-events.ndjson`, `p1-image.log`, `p1-binary-sizes.log`, `p1-toolchain.log`, `p1-clone-state.log`, `p1-integration-test.log`, `p1-browser-up.log`, `p1-browser-send.log`, `p1-browser-events.ndjson`, `p1-commits.log`, `p1-all-commits.log`.
- **Total commits since P1 plan landed (`c757bad..HEAD`):** 24.

```
1fc7d08 Add cspace2 lifecycle integration test
d9314cd cspace2-up --browser: auto-orchestrate Playwright sidecar lifecycle
17b3b62 cspace2-up auto-provisions per-sandbox git clone as /workspace
d9b49d4 Bake dev toolchain into cspace2 image (Go, pnpm, build-essential, rg, jq)
a154ec1 Substrate: add HealthCheck (apiserver preflight) and call from cspace2-up
271e23c Add cspace init-keychain: prompt for credentials into macOS Keychain
778a3be Add macOS Keychain layer to secrets resolver
75884f4 Registry-daemon: idle shutdown + cspace registry-daemon stop/status
4323b48 Rename prototype-* commands to cspace2-*; image to cspace2:latest
7a74f16 Use sandboxmode for in-sandbox config-load skip
e5694b3 Add internal/sandboxmode: detect in-sandbox execution from env
bc014cc Spike idle-survival; live event stream tooling + finding resolved
1b1ccb4 Spike browser sidecar pattern; Playwright MCP drives Chromium via CDP
7be1fce First real-scenario test PASS; track missing-toolchain finding
c3ad5e2 Spike Claude tool surface; non-root + bypassPermissions = tools fire
b454261 Unblock Claude auth in sandboxes: --dns injection + OAuth token alias
84a787e Spike per-sandbox git clone as /workspace; design verified
f171f86 Spike script: verify Claude auth via ANTHROPIC_API_KEY in sandbox
cf73e52 Spike multi-turn Claude persistence; PASS, finding resolved
9f0e2f4 Untrack cspace-context .lock files; gitignore the pattern
d22def5 P1 plan: extend Task 8 with verified DNS injection via RunSpec.DNS
f574a7c Track sandbox DNS finding in cspace-context
8635903 Spike Playwright under Apple Container; verify Chromium launches in microVM
0807f7a Spike GitHub access from inside sandbox; add gh CLI to prototype image
```

## Cutover checklist (post-P1, NOT P1 work)

When ready to flip `cspace2-*` -> `cspace *`:

- [ ] Resolve the `vminitd` env-leak finding (or document why deferred is acceptable).
- [ ] Add `cspace registry prune` for legacy `prototype-*` / `cspace2-*` entries.
- [ ] Decide on git-lfs vs build-on-demand for the supervisor binary.
- [ ] Build a custom lean browser image OR accept the cold-start cost.
- [ ] Run the final integration test suite (`TestCspace2Lifecycle` + Task 9 toolchain test + Task 11 browser test).
- [ ] Rename: `cspace2-up` -> `cspace up`, etc. Container prefix `cspace2-` -> `cspace-`. Image tag `cspace2:latest` -> `cspace:latest`.
- [ ] Delete legacy code: `internal/compose/`, `internal/docker/`, `internal/instance/`, `internal/provision/`, `lib/templates/Dockerfile`, `lib/templates/docker-compose*.yml`, `lib/agent-supervisor/` (Node).
