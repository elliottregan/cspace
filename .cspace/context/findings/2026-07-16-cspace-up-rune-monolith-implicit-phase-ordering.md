---
title: cspace up's RunE is an ~875-line monolith with comment-enforced phase ordering
date: 2026-07-16
kind: finding
status: open
category: refactor
tags: cmd-up, structure, lifecycle, cleanup
---

## Summary
`cmd_up.go`'s RunE (`cmd_up.go:56-931`) performs ~15 phases inline: daemon spawn, credential reconciliation, devcontainer merge, image-staleness prompt, clone provisioning, browser sidecar, registry writes, substrate run, IP/health polling, DNS resolution gate, auto-attach. Ordering constraints are enforced only by comments ("must run BEFORE the overlay", "BEFORE the containerEnv merge", "Snapshot … BEFORE the compose env_file merge"), and teardown is deferred conditionally on the **named return value** `err` (`cmd_up.go:667-673`, `808-812`) — an early `return` that forgets to assign `err` silently skips sidecar/container cleanup and leaks microVMs.

## Details
- This function is where the project's recurring env-precedence bugs live (see the env-precedence finding); every one of them traced to implicit ordering inside this flow.
- Suggested direction: decompose into explicit phase functions operating on a boot-context struct, with cleanup registered per acquired resource (a small teardown stack) instead of err-conditional defers. Do it **after** the env-resolver extraction (that removes a large chunk of the function and is independently valuable) and land it as a behavior-preserving refactor with the existing tests as the gate.
- Risk note: high-churn file; refactor should be its own PR with no behavior changes mixed in.

## Updates
### 2026-07-17T03:42:21Z — @agent — status: open
filed from the 2026-07-16 hardening survey
