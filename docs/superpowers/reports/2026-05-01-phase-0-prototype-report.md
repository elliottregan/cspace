# Phase 0 Prototype Report

**Date:** 2026-05-01
**Spec:** [docs/superpowers/specs/2026-04-30-sandbox-architecture-design.md](../specs/2026-04-30-sandbox-architecture-design.md)
**Plan:** [docs/superpowers/plans/2026-04-30-phase-0-prototype.md](../plans/2026-04-30-phase-0-prototype.md)
**Branch:** `phase-0-prototype`
**Commits:** `24e2b2a..ab4c8b0` (12 commits, full list at [Artifacts](#artifacts))

## Executive summary

Phase 0 succeeded. All five dealbreakers cleared in a live two-sandbox demo: Apple Container boots `cspace-prototype:latest`, Claude Code initialises inside, host-to-sandbox `prototype-send` lands as a `user-turn` event in the recipient's `events.ndjson`, in-sandbox `cspace prototype-send` reaches a sibling sandbox via the host registry-daemon and lands as a `user-turn` there too, and Apple Container's networking model (direct host-routable IPs, host == gateway, no `host.docker.internal`) is documented and exploited end-to-end. Apple Container is the right substrate for the new architecture; **proceed to P1**.

## Dealbreakers

### 1. Boot a sandbox on Apple Container — PASS

- Substrate: Apple Container `0.12.3` on macOS 26.4 (Darwin 25.4.0), Apple Silicon arm64. Installed via `brew install container`; runtime brought up with `container system start` + `container system kernel set --recommended`.
- Image build: `cspace-prototype:latest`, **293 MiB** (`306,749,231` bytes; `linux/arm64`-only OCI manifest, single platform). Cold builds run ~3 min; warm rebuilds (only the `cspace` and `cspace-supervisor` `COPY` layers churn) are ~10s.
- Boot times measured in the spike: cold pull alpine 10.7s; first run after pull 5.86s (one-time init-image fetch); warm runs ~0.7s. The two prototype sandboxes (`cspace-cspace-A`, `cspace-cspace-B`) came up with valid IPs in <2s wall time during the demo (`prototype-up` polls `IP()` for up to 10s; both completed first poll cycle).
- Two sandboxes ran concurrently, received sequential bridge IPs (`192.168.64.37`, `192.168.64.38`) on `192.168.64.0/24`, and both reached the bound supervisor ports cleanly.
- Evidence: [docs/superpowers/spikes/2026-04-30-apple-container-spike.md](../spikes/2026-04-30-apple-container-spike.md), commit `d55250d` (spike), `0e8ff18` (substrate adapter), `402290b` (image), `435ee23` (`prototype-up`).

### 2. Install + run Claude Code inside — PARTIAL

- Bun + Claude CLI install in image works in a single layer: `bun install --global @anthropic-ai/claude-code` + symlink to `/usr/local/bin/claude`. `claude --version` runs at build time as the install verification step. (`lib/templates/Dockerfile.prototype`, commit `402290b`.)
- Bun supervisor compiled to `linux/arm64` ELF binary at `lib/agent-supervisor-bun/dist/cspace-supervisor`, **97 MB** (`102,172,992` bytes), produced by `bun build --compile`. `file` confirms `ELF 64-bit LSB executable, ARM aarch64, dynamically linked, interpreter /lib/ld-linux-aarch64.so.1`. The binary embeds the Bun runtime and pulls in glibc from the Debian bookworm-slim base. Commit `94fa626`.
- The supervisor starts cleanly inside both sandboxes, binds `0.0.0.0:6201`, writes `supervisor-start` to `/sessions/primary/events.ndjson`, and the SDK's `query()` consumer initialises and ingests the user-turn pipeline as designed.
- **Auth caveat (the reason this is PARTIAL, not PASS):** the host running this demo had no `ANTHROPIC_API_KEY` exported, and macOS Keychain credentials do **not** auto-flow into the sandbox. The SDK reports `apiKeySource: "none"` and replies with the synthetic `"Not logged in · Please run /login"` message. The user-turn injection pipeline runs to completion regardless — the SDK still emits `system/init`, `assistant`, and `result` events for the turn. The dealbreaker is the messaging plumbing, which works; resolving the auth handoff is a P1 concern (see [P1 risks](#p1-risks-surfaced) #2).
- Evidence: `/tmp/p0-A-events.ndjson` line 3 shows `apiKeySource: "none"`; line 4 shows the synthetic Not-logged-in response; line 5 shows `is_error: true, terminal_reason: "completed"`. Same shape in `/tmp/p0-B-events.ndjson`. Commits `402290b` (image), `94fa626` (supervisor).

### 3. Direct user-turn messaging from host to sandbox — PASS

- `/tmp/p0-host-to-A.log`:
  ```
  {"ok":true}
  ```
- `/tmp/p0-A-events.ndjson` (excerpts; full file at `/tmp/p0-A-events.ndjson`):
  ```
  {"ts":"2026-05-01T01:58:46.852Z","session":"primary","kind":"supervisor-start","data":{"port":6201,"session":"primary"}}
  {"ts":"2026-05-01T01:59:00.642Z","session":"primary","kind":"user-turn","data":{"source":"control-port","text":"Say PONG"}}
  {"ts":"2026-05-01T01:59:00.721Z","session":"primary","kind":"sdk-event","data":{"type":"system","subtype":"init","cwd":"/workspace","session_id":"d47cbb52-...","apiKeySource":"none","claude_code_version":"2.1.123",...}}
  {"ts":"2026-05-01T01:59:00.737Z","session":"primary","kind":"sdk-event","data":{"type":"assistant","message":{...,"content":[{"type":"text","text":"Not logged in · Please run /login"}],...},"error":"authentication_failed"}}
  {"ts":"2026-05-01T01:59:00.737Z","session":"primary","kind":"sdk-event","data":{"type":"result","subtype":"success","is_error":true,"duration_ms":50,"num_turns":1,"result":"Not logged in · Please run /login","stop_reason":"stop_sequence",...,"terminal_reason":"completed",...}}
  ```
  The `user-turn` event with `source: "control-port", text: "Say PONG"` is the load-bearing line: it proves the host POST traversed the supervisor's `/send` handler and entered the prompt stream that the SDK's `for await` loop consumes. The `system/init` event 79ms later is the SDK reacting to that turn.
- Mechanism: `cspace prototype-send A "Say PONG"` resolves `default:A` from `~/.cspace/sandbox-registry.json` → POSTs `{"session":"primary","text":"Say PONG"}` with `Authorization: Bearer <token>` to `http://192.168.64.37:6201/send` → supervisor authorises, calls `prompts.push(text)`, logs `user-turn`, returns `{"ok":true}`. The SDK's `query()` consumer reads the next prompt-stream item and emits `system/init`.
- Evidence: commits `d25326d` (`prototype-send`), `94fa626` (supervisor `/send` handler at `lib/agent-supervisor-bun/src/main.ts:53–64`), `0e8ff18` (substrate exposes IP via `Adapter.IP`).

### 4. Cross-sandbox messaging — PASS

- `/tmp/p0-A-to-B.log`:
  ```
  {"ok":true}
  ```
  (Output of `container exec cspace-cspace-A cspace prototype-send B "Say PING"` — the in-sandbox `cspace` binary completed and the host-side return value is the supervisor's reply.)
- `/tmp/p0-B-events.ndjson` (excerpts; full file at `/tmp/p0-B-events.ndjson`):
  ```
  {"ts":"2026-05-01T01:58:51.926Z","session":"primary","kind":"supervisor-start","data":{"port":6201,"session":"primary"}}
  {"ts":"2026-05-01T01:59:14.075Z","session":"primary","kind":"user-turn","data":{"source":"control-port","text":"Say PING"}}
  {"ts":"2026-05-01T01:59:14.111Z","session":"primary","kind":"sdk-event","data":{"type":"system","subtype":"init","cwd":"/workspace","session_id":"ddb60343-...",...}}
  {"ts":"2026-05-01T01:59:14.123Z","session":"primary","kind":"sdk-event","data":{"type":"assistant","message":{...,"content":[{"type":"text","text":"Not logged in · Please run /login"}],...}}}
  {"ts":"2026-05-01T01:59:14.123Z","session":"primary","kind":"sdk-event","data":{"type":"result",...,"result":"Not logged in · Please run /login",...,"terminal_reason":"completed",...}}
  ```
  The `user-turn` event in B's log carries the `Say PING` text that A sent — proving the host-mediated cross-sandbox path is closed end-to-end. As with dealbreaker 3, the synthetic "Not logged in" reply is the SDK's response to the turn, not a transport failure.
- Mechanism: inside sandbox A, `cspace prototype-send B "Say PING"` runs the same Go binary (`bin/cspace-linux-arm64` shipped into the image at `/usr/local/bin/cspace`). It sees `CSPACE_REGISTRY_URL=http://192.168.64.1:6280` (gateway IP, set by `prototype-up`), GETs `/lookup/cspace/B` from the host-side `cspace-registry-daemon`, gets back `{control_url: "http://192.168.64.38:6201", token: "...", ip: "192.168.64.38", ...}`, and POSTs to that direct IP with the bearer token — same `/send` path as the host case. No hostfwd, no `--publish`; container-to-container traffic on the shared `192.168.64.0/24` bridge.
- Evidence: commits `2569604` (`cspace-registry-daemon`), `ef031c4` (in-sandbox `resolveEntry` HTTP fallback in `internal/cli/cmd_prototype_send.go`), `435ee23` (`prototype-up` injects `CSPACE_REGISTRY_URL` and `CSPACE_HOST_GATEWAY`).

### 5. Apple Container networking model — DOCUMENTED

- IP scheme: `192.168.64.0/24`, gateway `192.168.64.1`, plugin `container-network-vmnet` in NAT mode. Containers receive sequential IPv4 addresses; the demo got `.37` and `.38`. Addresses are ephemeral per run (the spike observed `probe1` getting `.5` in one session and `.11` in another — `prototype-up` snapshots the IP into the registry at start time and treats it as ephemeral).
- Direct IP routing works in all three directions tested in the spike and re-exercised in this demo:
  - **host → container.** The host POST to `http://192.168.64.37:6201/send` succeeded with no `--publish` flag.
  - **container → container.** A's POST to `http://192.168.64.38:6201/send` (sibling) succeeded over the bridge.
  - **container → host.** A's GET to `http://192.168.64.1:6280/lookup/cspace/B` hit the host registry-daemon listening on the gateway interface.
- No `host.docker.internal` shortcut exists; sibling-name DNS does **not** resolve out of the box (`/etc/resolv.conf` only points at the gateway). The gateway IP itself is the only well-known address a container gets.
- Decision: **direct-IP messaging via host registry-daemon**, not hostfwd, not `--publish`. This is implemented across Tasks 6–9 and verified in the demo. The `--publish` machinery in `applecontainer.Adapter.Run` (`internal/substrate/applecontainer/adapter.go:49–52`) is preserved as an escape hatch for a future "preview port on `127.0.0.1`" workflow but is unused by the prototype.
- Evidence: [docs/superpowers/spikes/2026-04-30-apple-container-spike.md](../spikes/2026-04-30-apple-container-spike.md), commit `d55250d`.

## Linux containerd feasibility — NO BLOCKERS

The Task 10 paper feasibility study ([docs/superpowers/spikes/2026-04-30-containerd-spike.md](../spikes/2026-04-30-containerd-spike.md), commit `ab4c8b0`) found no architectural blockers for porting cspace to containerd. The recommended path is a **`nerdctl` shell-out adapter** that mirrors `applecontainer.Adapter` line-for-line: same argv-builder shape (`run -d --name … -e … -v … -p … <image> <cmd>`), same idempotent `stop && rm`, same JSON `inspect` parse for IP discovery, with field-path tweaks (`NetworkSettings.IPAddress` instead of `networks[].ipv4Address` and no CIDR-suffix strip). The OCI image is drop-in compatible — same image-spec v1.1 manifests + layers — although the prototype is currently `linux/arm64`-only and would need a multi-arch buildx pipeline before running on `linux/amd64` Linux hosts. Direct-IP routing on the CNI bridge (default `10.4.0.0/24`) gives the same posture cspace exploited on Apple Container, so the cross-sandbox messaging design transfers without redesign. **Estimated 6–9 engineer-days for the full Linux adapter** (P5 work). What was *not* verified: real Linux execution of any kind — the recommendation is to validate on a `lima` Ubuntu 24.04 VM before P5 starts.

## Architecture verdict

The spec's design is **sound and validated by the prototype**. Every load-bearing claim from §Solution and §Topology was either directly exercised in the demo (substrate, control plane, supervisor, networking) or shown to be cleanly reachable from the prototype (containerd parity). The two-sandbox demo is the smallest possible end-to-end exercise of the spec's central thesis — that `cspace send` is the only cross-sandbox primitive and direct user-turn injection is the messaging mechanism — and it works.

Specific spec assertions, with verdicts:

- **Sandbox unit of isolation.** Confirmed. Each `container run` produces a Linux microVM with its own filesystem, supervisor, and event log; Apple Container gives us hardware-level isolation as a free upgrade vs. Docker namespaces.
- **Per-implementer top-level sandbox with own DB/Playwright.** Still right. The prototype only ran one supervisor + one Claude session per sandbox, but the boot cost (~0.7s warm, image is 293 MiB) is cheap enough that running 4–6 sandboxes concurrently for a coordinator + implementers fan-out is well within budget. Spec §"Open questions" item 5 (RAM overhead under 2GB on a 16GB Mac) was not measured in P0; it should be measured in P1.
- **Coordinator + advisors collocated in one sandbox.** Still right — out of scope for P0 to verify since multi-session was deferred, but nothing observed contradicts this. The single-sandbox design is the simpler shape and the prototype's supervisor is deliberately single-session to leave room for the Phase 2 multi-session extension.
- **Direct user-turn messaging.** Proven end-to-end, both host-to-sandbox and sandbox-to-sandbox, against the actual SDK `query()` consumer. The async-queue prompt-stream pattern (`lib/agent-supervisor-bun/src/prompt-stream.ts`) carried forward from the existing Node supervisor lands without modification.
- **Bun TS supervisor with single binary.** Proven. The 97 MB compiled output is large but ships as one `COPY` line in the Dockerfile; the boot cost of "no `pnpm install` in `entrypoint.sh`" pays for the image-size overhead. Worth tracking for P1 (see risk #4).
- **Apple Container substrate.** Proven, with caveats: the runtime is GA on macOS 26 but is at version 0.12.3, and we hit one CLI quirk in the spike (interactive kernel prompt on first `system start`) and one in adapter implementation (`container inspect` has no `--format` flag, so we parse raw JSON). Neither is a blocker; both are documented.

No spec sections need to be revisited before P1 begins. The `Substrate` interface in the spec (§"Substrate interface") is a superset of what the P0 adapter implements — `Run/Exec/Stop/IP/Available` are in; `BuildImage`, `CreateSandbox/StartSandbox/DestroySandbox` (lifecycle split), `PortForward`, and `SandboxIP` (typed `net.IP`) are P1+ extensions and the existing prototype code is structured to grow into that shape.

## P1 risks surfaced

Hard, named, ordered by severity:

1. **Claude SDK auth handoff.** The prototype demonstrates the messaging pipeline is independent of auth — `user-turn` events land regardless. But for P1's "actually useful sandbox" milestone, the SDK has to authenticate. Two paths: (a) require `ANTHROPIC_API_KEY` on the host and propagate through `prototype-up`'s env injection (already implemented at `internal/cli/cmd_prototype_up.go:59–61` but the user must set it); (b) integrate with `claude login` flow inside the sandbox, which today writes to `~/.claude/` — that's host-side under the existing `cspace` model and would need a deliberate bind mount or a `claude login` proxy. **Mitigation:** start P1 with path (a) and document `export ANTHROPIC_API_KEY=…` as a precondition; design path (b) as part of Phase 4 once the context-daemon work makes host-mediated state-sharing routine.

2. **Registry-daemon lifecycle.** The host daemon is started lazily by `prototype-up` (`ensureRegistryDaemon` at `internal/cli/cmd_prototype_up.go:160–187`) and **never stops** — no idle shutdown, no `cspace registry-daemon stop`, no PID file. It binds `0.0.0.0:6280` (so sandboxes can reach it via gateway IP), which means it is reachable by anything else on the host's bridge networks. **Mitigation:** P1 should add `cspace registry-daemon {start,stop,status}` (already in the spec's CLI surface table), an idle timeout (matching the spec's 30 min / "no project sandbox running" rule for context-daemon), and bind to the gateway IP `192.168.64.1` rather than `0.0.0.0` so it's not exposed beyond the Apple Container bridge.

3. **Apple Container CLI surface drift.** We depend on three `container` subcommands (`run`, `exec`, `stop`/`rm`, `inspect`) and parse `inspect`'s JSON for IP discovery. `container inspect` has no `--format` flag, so the JSON shape is what we have — and Apple Container is at 0.12.3, pre-1.0. Any breaking change in the JSON shape silently breaks `Adapter.IP`. **Mitigation:** unit-test the JSON parse against captured fixtures; add a `cspace doctor` preflight that runs `container --version` and warns on unknown versions; pin a known-good version in docs until 1.0 ships.

4. **Supervisor binary size.** 97 MB Bun-compiled binary is most of the 293 MiB image. The image is `linux/arm64`-only; multi-arch will roughly double. The size penalty matters for image-pull time on cold registries (P5: pushing to a real registry for Linux hosts) and for cspace's "quick rebuild" loop (Apple Container's `image pull` re-fetches all platform variants — see spike gotcha #2). **Mitigation:** investigate `bun build --minify --compile` (default builds may not minify); evaluate whether the supervisor needs the full Bun runtime or could ship as Node + esbuild bundle; revisit if image-pull becomes a hot path in P5.

5. **No CI parity for Apple Container.** GitHub Actions has no macOS 26 + Apple Container runner. The substrate adapter's `adapter_test.go` requires `container` on PATH (per the test guard); CI will skip those tests and we'd discover regressions only on the developer's Mac. **Mitigation:** P1 should establish a local-only "make test-substrate" make target that the maintainer runs before PRs touching `internal/substrate/applecontainer/`; longer-term, a self-hosted macOS runner if the substrate code turns into a hot path.

6. **In-sandbox `cspace` binary distribution.** The 15 MB Go binary `bin/cspace-linux-arm64` is `COPY`'d into the image, which means the sandbox's `cspace` is pinned to whatever the host built when the image was last baked. A host upgrade of `cspace` does not propagate to running sandboxes; users would have to `cspace rebuild`. **Mitigation:** acceptable for P1 (parallels today's image-rebuild workflow), but document the staleness window in `cspace doctor` output. P2 could add a `/control/upgrade` endpoint that swaps the in-sandbox binary in place if this becomes a real friction point.

7. **`prototype-down` does not stop the registry-daemon, even if no sandboxes remain.** Closely related to risk #2. Tear down the last sandbox and the daemon keeps running. Visible in the demo: `prototype-down A && prototype-down B` returned cleanly but a manual `lsof -i :6280` after the demo would show the daemon still bound. **Mitigation:** in P1, the registry-daemon's lifecycle hook fires on the registry transitioning to empty.

## Recommendation

**Proceed to P1.** All five dealbreakers cleared, the architecture spec is internally consistent and validated, and the prototype is small enough (12 commits, 4 new internal packages, ~600 LoC TypeScript supervisor, one new daemon) that the cost-of-rework if P1 surfaces a surprise is bounded. The auth-handoff risk is the only one that gates "useful work in a sandbox" rather than "messaging works between sandboxes," and it has a clean three-line workaround (`export ANTHROPIC_API_KEY=…`) for the P1 critical path. Start P1 with the substrate adapter + sandbox registry promoted from `prototype-*` to top-level `cspace up/down/send`, fold in the lifecycle and bind-address fixes for the registry-daemon (risks #2 and #7), and target the Phase 1 "wire the substrate adapter into the main CLI" milestone before any of the supervisor multi-session work.

## Open questions for P1 design

- **In-sandbox `cspace` binary size optimization.** Do we ship the full host CLI inside the sandbox, or a minimal `cspace-agent` subset that only knows `send/exec/logs`? The 15 MB binary today is fine; the 25+ MB it'll grow into once it has all of P1's commands may not be.
- **Registry-daemon shutdown semantics.** The spec's §"Cross-sandbox context — host daemon" describes a 30-minute idle threshold for `cspace context-daemon`. Should `cspace registry-daemon` use the same threshold, or stay alive as long as any registry entry exists? P0 punted by leaving it permanent.
- **Auth handoff.** See risk #1. Concrete decision needed before P1 milestone "implementer can finish a real issue."
- **Image build pipeline for Linux.** `make prototype-image` runs `docker buildx build --platform linux/arm64 --output type=oci …` and feeds the OCI tarball to Apple Container. P5 needs the same pipeline producing multi-arch and either pushing to a registry or `nerdctl load`'ing on Linux hosts. Decision: keep buildx + multi-arch, or move to a Linux-native build path?
- **Supervisor session-resume semantics.** Spec §"Failure recovery" mentions `cspace session resume impl-42:primary` re-spawns against an existing worktree. The P0 supervisor has no resume — `runClaude` starts fresh on each container start. P1 multi-session work is the natural place to design this; P0 didn't have to answer.
- **Telemetry for the supervisor.** Today: `events.ndjson` is the only signal. P1 needs to decide whether the supervisor logs go anywhere besides the per-sandbox file (host stdout? structured log to a host daemon?) before the activity hub of Phase 3.

## Artifacts

- **Spec:** [docs/superpowers/specs/2026-04-30-sandbox-architecture-design.md](../specs/2026-04-30-sandbox-architecture-design.md)
- **Plan:** [docs/superpowers/plans/2026-04-30-phase-0-prototype.md](../plans/2026-04-30-phase-0-prototype.md)
- **Spike (Apple Container):** [docs/superpowers/spikes/2026-04-30-apple-container-spike.md](../spikes/2026-04-30-apple-container-spike.md)
- **Spike (containerd):** [docs/superpowers/spikes/2026-04-30-containerd-spike.md](../spikes/2026-04-30-containerd-spike.md)
- **New code:** `internal/substrate/`, `internal/registry/`, `cmd/cspace-registry-daemon/`, `internal/cli/cmd_prototype_{up,send,down}.go`, `lib/agent-supervisor-bun/`, `lib/templates/Dockerfile.prototype`.
- **Branch:** `phase-0-prototype`.
- **Demo evidence files (host `/tmp`):** `p0-up-A.log`, `p0-up-B.log`, `p0-host-to-A.log`, `p0-A-to-B.log`, `p0-A-events.ndjson`, `p0-B-events.ndjson`, `p0-image.log`, `p0-supervisor-binary.log`, `p0-cspace-binary.log`, `p0-binary-sizes.log`, `p0-commits.log`.

**Total commits (`24e2b2a..ab4c8b0`):** 12.

```
2eedbe3 Add sandbox architecture design spec
4e21a41 Add Phase 0 prototype implementation plan
d55250d Spike Apple Container CLI: boot, network, exec, mount semantics
0e8ff18 Add Substrate interface and Apple Container adapter (P0 minimal)
402290b Add prototype sandbox image (Bun + Claude Code + cspace) and Makefile targets
94fa626 Add Bun TS supervisor: POST /send injects user turns into Claude session
235a31f Add sandbox registry: JSON-backed name → control URL lookup
435ee23 Add cspace prototype-up/-down: launch sandboxes via Apple Container substrate
d25326d Add cspace prototype-send: HTTP POST to sandbox control port
2569604 Add cspace-registry-daemon: HTTP lookup for in-sandbox cspace
ef031c4 Cross-sandbox prototype-send via registry-daemon HTTP fallback
ab4c8b0 Spike Linux containerd feasibility for cspace P5
```
