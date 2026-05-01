---
title: Sandbox image lacks dev toolchain (Go, etc.); agent burns turns installing
date: 2026-05-01
kind: finding
status: resolved
category: refactor
tags: sandbox-image, p1, dev-tooling, agent-experience
related: lib/templates/Dockerfile.prototype, lib/templates/Dockerfile, docs/superpowers/plans/2026-05-01-phase-1-canonical-cspace2.md
---

## Summary
First real-scenario agent task (write a test for `registry.DefaultPath()`, run it, commit) succeeded end-to-end on the cspace2 prototype, but the agent had to spend ~5 minutes (8+ tool calls) downloading and installing Go from go.dev because the prototype image ships with neither Go nor any other project dev toolchain. The legacy cspace `Dockerfile` installs Go, Node, pnpm, ripgrep, python3, etc. up front; `Dockerfile.prototype` was kept minimal during P0 dealbreaker-verification and never grew those dependencies back. P1's `Dockerfile.cspace2` should restore them so agents stop self-bootstrapping each session.

## Details


## Updates
### 2026-05-01T05:21:20Z — @agent — status: open
filed

### 2026-05-01T06:32:28Z — @agent — status: resolved
## Resolved in P1 Task 9

Toolchain baked into `lib/templates/Dockerfile.cspace2` directly. The image now ships:

- Go 1.23.4 (official linux/arm64 tarball, unpacked to `/usr/local/go`, `PATH` and `GOPATH=/home/dev/go` set, `/home/dev/go` chowned to dev)
- `make`, `python3`, `build-essential`, `ripgrep`, `jq` (apt, single layer alongside the existing `ca-certificates curl unzip git tini sudo` block)
- `pnpm@9` (npm global, installed right after Node.js so it's on PATH for both root and dev)

Layer ordering: dev toolchain sits high in the Dockerfile (rarely-changing) so the more volatile bits (Bun / Claude Code / MCP servers / supervisor binary) keep cache hits. Image size grew from ~405 MB to ~540 MB — well within the ~600 MB budget the original finding suggested.

End-to-end verified: agent prompted to run `go test ./internal/registry/...` (Task 9 step 8 smoke).
- `go test invoked: True`
- `go install attempted: False` (the litmus)
- `result is_error: False`, `result subtype: success`
- `num_turns: 2` (vs the P0 baseline ~23 turns the install detour caused)
- duration_ms: 25766

Smoke test inside the image as the dev user prints expected versions for go (1.23.4 linux/arm64), make (4.3), python3 (3.11.2), gcc (12.2.0), rg (13.0.0), jq (1.6), pnpm (9.15.9), node (20.20.2), claude (2.1.126), gh (2.92.0).

Closing as resolved. If a project ever needs additional tooling (lefthook, golangci-lint, etc.), per-project bakes belong in `.cspace/` init rather than the shared base image.
