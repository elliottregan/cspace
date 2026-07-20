---
title: TUI down action reports benign teardown warnings as a failed action
date: 2026-07-20
kind: finding
status: open
category: bug
tags: tui, actor, down, teardown, ux
---

## Summary
The TUI's `down` actor (internal/cli/tui_actor.go) flags the action as failed whenever `teardownSandbox`'s captured output contains the substring `warning:`. But `teardownSandbox` emits `[cspace] warning:` lines for benign secondary issues (leftover volume removal, sidecar teardown, session-dir cleanup) even when the sandbox itself stopped cleanly — so a fully-successful teardown can render a red "down failed: …" notice in the footer. Surfaced by the cspace TUI final whole-branch review (PR #92, branch `feat/cspace-tui`).

## Details
- Root cause: `teardownSandbox` (internal/cli/cmd_down.go) has no return value and swallows the container `Stop` error (`_ = a.Stop(...)`); its only failure signal is warning text on the passed `io.Writer`. The actor's `strings.Contains(buf.String(), "warning:")` heuristic can't distinguish a fatal Stop failure from benign cleanup noise.
- This was a deliberate plan choice: surface warnings rather than report a false "down ok" (the alternative — unconditional success — hides real failures). The wart is that benign warnings now read as failure.
- Fix direction (if wanted): give `teardownSandbox` a typed return (e.g. distinguish "sandbox Stop failed" from "cleanup produced warnings"), then have the actor flag only a fatal Stop as a failed action and show cleanup warnings as an informational (non-error) notice. This also improves the CLI `cspace down` exit signal, which today is always success.

## Updates
### 2026-07-20T08:44:02Z — @agent — status: open
Filed from the cspace TUI final whole-branch review (PR #92). Non-blocking; refinement deferred because it requires changing `teardownSandbox`'s signature (shared with the CLI down path).
