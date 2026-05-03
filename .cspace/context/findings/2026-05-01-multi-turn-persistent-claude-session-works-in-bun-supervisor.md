---
title: Multi-turn persistent Claude session works in Bun supervisor (verified)
date: 2026-05-01
kind: finding
status: resolved
category: observation
tags: verification, supervisor, claude-sdk, p0-extension, persistent-mode
related: scripts/spikes/2026-05-01-multi-turn-persistence.sh, scripts/spikes/2026-05-01-multi-turn-persistence.py, lib/agent-supervisor-bun/src/prompt-stream.ts, docs/superpowers/specs/2026-04-30-sandbox-architecture-design.md
---

## Summary
Open question raised during P1 review: does the new Bun supervisor's PromptStream wrapper around `query()` actually keep ONE Claude session alive across multiple injected user turns, or does each `/send` create a fresh session? Tested via `scripts/spikes/2026-05-01-multi-turn-persistence.sh`: 5 turns sent in sequence to the same sandbox, all landed as `user-turn` events, all triggered full SDK `init → assistant → result` cycles, all under a single `session_id`, zero errors. Persistent-mode messaging is preserved by the new architecture. Resolved on first verification — keeping for the audit trail since this was a load-bearing assumption for the entire P1 design.

## Details
## Test method

`scripts/spikes/2026-05-01-multi-turn-persistence.sh` boots a fresh sandbox, sends 5 distinct user turns via `cspace prototype-send` (4-second gap between each so Claude completes each turn), then inspects `/sessions/primary/events.ndjson` via the companion parser at `scripts/spikes/2026-05-01-multi-turn-persistence.py`.

## Result

```json
{
  "user_turns": 5,
  "sdk_inits": 5,
  "sdk_assistants": 5,
  "sdk_results": 5,
  "sdk_errors": 0,
  "distinct_session_ids": [
    "bdc2c69a-54ed-4201-ac8a-63e49f25bbf9"
  ]
}
```

All five turns landed in the single live session. The SDK's `init/assistant/result` triplet fires per turn (turn-completion, not session-end), and the iterator goes back to waiting for the next prompt push between turns.

## What this proves and does NOT prove

PROVES:
- `PromptStream`'s async-iterable contract correctly preserves session continuity across multiple pushes.
- `runClaude`'s `for await` loop in `claude-runner.ts` does not exit between turns — it keeps consuming.
- The supervisor's HTTP server stays responsive across multiple `/send` requests against the same live session.
- No leaks of `sdk-error` between turns; no spurious session resets.

DOES NOT PROVE:
- That Claude actually *remembers* prior turns within the session at the model level. This run had no `ANTHROPIC_API_KEY` on the host, so all assistant responses were the synthetic "Not logged in · Please run /login" placeholder. Verifying conversational memory across turns requires re-running this script with auth and inspecting whether turn N's response references turn N-1's content. This is a Claude reasoning property, not a plumbing property — worth verifying once auth lands but not blocking on it.
- That the session survives long idle gaps (>5 min between turns). The 4-second inter-turn gap in this test is far below any plausible timeout. A dedicated idle-resilience test is Tier 1 #4 in the P1 review notes.

## POC concessions in the test artifacts

The shell+python script pair under `scripts/spikes/` is intentionally lo-fi:
- Hardcoded sandbox name `mt`; concurrent runs collide.
- Fixed `sleep` durations between turns; not robust against slow SDK init.
- Uses `python3` for ndjson parsing; not embedded in Go test machinery.

P1 Task 9 (`internal/cli/cmd_cspace2_integration_test.go`) is the polished home for this verification. Until P1 lands, the script lives here as proof-of-concept evidence for the spec's persistent-mode claim.

## Implications for P1

None — P1 plan stands as written. This finding moves directly to `resolved` because the question it answers is binary and now has a clean answer. Logged for the audit trail.

## Updates
### 2026-05-01T03:30:33Z — @agent — status: resolved
filed
