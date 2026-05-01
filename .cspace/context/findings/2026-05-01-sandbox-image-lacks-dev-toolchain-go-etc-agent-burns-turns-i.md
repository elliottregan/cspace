---
title: Sandbox image lacks dev toolchain (Go, etc.); agent burns turns installing
date: 2026-05-01
kind: finding
status: open
category: refactor
tags: sandbox-image, p1, dev-tooling, agent-experience
related: lib/templates/Dockerfile.prototype, lib/templates/Dockerfile, docs/superpowers/plans/2026-05-01-phase-1-canonical-cspace2.md
---

## Summary
First real-scenario agent task (write a test for `registry.DefaultPath()`, run it, commit) succeeded end-to-end on the cspace2 prototype, but the agent had to spend ~5 minutes (8+ tool calls) downloading and installing Go from go.dev because the prototype image ships with neither Go nor any other project dev toolchain. The legacy cspace `Dockerfile` installs Go, Node, pnpm, ripgrep, python3, etc. up front; `Dockerfile.prototype` was kept minimal during P0 dealbreaker-verification and never grew those dependencies back. P1's `Dockerfile.cspace2` should restore them so agents stop self-bootstrapping each session.

## Details
## What was observed

Prompted the agent (in a non-root sandbox with the workspace clone bind-mounted) to:
1. Read the existing tests in `internal/registry/registry_test.go`.
2. Add a `TestDefaultPath` function.
3. Run `go test ./internal/registry/...` and verify it passes.
4. Commit on `cspace/scenario1`.

The agent did all of that successfully — but step 3's first attempt failed because `go` is not installed. The agent then spent 8 turns (steps 6–19 of the tool-use trace) on:
- `which go`, `find / -name go`, `ls /usr/local/`, `ls /opt/` — locating the missing toolchain.
- `apt-get install golang-go` — failed (no apt cache).
- `curl -fsSL https://go.dev/dl/go1.23.0.linux-amd64.tar.gz` — downloaded the wrong arch first, then realized it from `uname -m`, retried with `linux-arm64`.
- `sudo tar -C /usr/local -xzf …` — installed Go.
- Resumed the original task: `go test ./...`, `git commit`.

Total agent wall time: 119s (`result.duration_ms`). Roughly 60s of that was the bootstrap detour.

## Why this matters

- Every agent in every sandbox pays this tax on the first Go invocation. With multiple implementer sandboxes per coordinator session, that's significant token spend.
- It also depends on `go.dev` being reachable and the public DNS we inject working — if either is wrong, the agent is stuck.
- The legacy cspace image already installs `go make pnpm node python3 ripgrep` etc. at build time (`lib/templates/Dockerfile` lines 11–27). Re-baking those into `Dockerfile.cspace2` is the obvious fix.
- The agent demonstrated correct judgment ("missing dep is not a bug, install it"); we don't want to remove that capability, just not require it for routine work.

## Implications for P1

`docs/superpowers/plans/2026-05-01-phase-1-canonical-cspace2.md` already plans to swap `Dockerfile.prototype` → `Dockerfile.cspace2` (Task 3, image rename). That swap is the right place to also restore the dev toolchain. Concretely:

1. Add to the apt install list in `Dockerfile.cspace2`: `make python3 build-essential ripgrep jq`.
2. Install Go via the official tarball method the agent used (deterministic version pin, e.g. `go1.23.4.linux-arm64.tar.gz`) into `/usr/local/go`. Add `/usr/local/go/bin` to PATH.
3. Install Node / pnpm at a pinned version (npm install -g pnpm@<ver>).
4. Optional: bake project-specific tooling that's used across most cspace projects (lefthook, golangci-lint, etc.) — TBD per project, may live in `.cspace/` per-project init script rather than the base image.
5. Verify image size impact (legacy is ~600 MB; current prototype is 254 MB; budget ~600 MB for the toolchain image).

## What is NOT a P1 concern from this finding

- Agent judgment / autonomy. Worked correctly.
- Tool surface. All the right tools were available; only the package availability was missing.
- Network reachability for non-Anthropic hosts. Verified by the successful go.dev download.

## POC concession from the scenario run

The scenario test itself was driven manually rather than via a polished spike script. The artifact (the agent's `TestDefaultPath` commit on the sandbox clone) is left in `~/.cspace/clones/cspace/scenario1/` for inspection; it's actually a real, valid test that could be merged into main if desired.

Status: open. Resolves when `Dockerfile.cspace2` ships with Go + Node + pnpm + python3 + standard build tooling baked in.

## Updates
### 2026-05-01T05:21:20Z — @agent — status: open
filed
