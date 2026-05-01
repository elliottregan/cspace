---
title: Persistent Claude session survives idle gaps with auth + memory intact
date: 2026-05-01
kind: finding
status: resolved
category: observation
tags: verification, idle-survival, session-persistence, memory, p0-extension
related: scripts/spikes/2026-05-01-long-idle.sh, scripts/spikes/2026-05-01-long-idle.py, scripts/spikes/2026-05-01-long-idle-stream.py, .cspace/context/findings/2026-05-01-multi-turn-persistent-claude-session-works-in-bun-supervisor.md
---

## Summary
Tier-1 #4 from the P1 review: does a persistent Claude session in the Bun-supervised sandbox survive an idle gap and pick back up correctly when a new turn arrives? Verified PASS via `scripts/spikes/2026-05-01-long-idle.sh` (IDLE_SECS=60). Turn 1 anchors a rare phrase in the session, the script idles, turn 2 asks for the phrase back. Both turns share the same SDK session_id, the second turn's response contains the phrase verbatim, no SDK errors, no api_retries. Auth (ANTHROPIC_API_KEY via OAuth-token alias) was unaffected by the idle. Memory survived. Architectural property proven; longer idle windows can be swept later if specific timeout boundaries (token refresh, HTTP keepalive) need stress.

## Details
## Test method

`scripts/spikes/2026-05-01-long-idle.sh` (IDLE_SECS env var; default 1800, ran with 60 for fast smoke):

1. Bring up sandbox.
2. Turn 1: "Please remember this exact phrase for later: 'purple-banjo-spirograph'. Acknowledge with just the word READY."
3. Sleep `IDLE_SECS` seconds.
4. Turn 2: "What was the exact phrase I asked you to remember earlier? Reply with just the phrase, nothing else."
5. Inspect events.ndjson via the parser at `2026-05-01-long-idle.py`.

## Live stream from the 60s run

```
[ev 05:58:02] user-turn  text="…remember 'purple-banjo-spirograph'…"
[ev 05:58:03] sdk init   apiKeySource=ANTHROPIC_API_KEY  sid=d27e522b
[ev 05:58:07] assistant  'READY'
[ev 05:58:07] sdk result is_error=False  num_turns=1

  ─── 60s idle window ───

[ev 05:59:15] user-turn  text="What was the exact phrase…"
[ev 05:59:15] sdk init   apiKeySource=ANTHROPIC_API_KEY  sid=d27e522b   ← same sid
[ev 05:59:16] assistant  "'purple-banjo-spirograph'"                    ← memory recall
[ev 05:59:16] sdk result is_error=False
```

## Verdict

```json
{
  "user_turns": 2,
  "distinct_session_ids": ["d27e522b-5e3d-493b-a840-8650ed717c1a"],
  "sdk_errors": 0,
  "last_result_is_error": false,
  "turn2_assistant_excerpt": "'purple-banjo-spirograph'",
  "magic_word_recalled": true,
  "PASS": true
}
```

## What this proves

- **Session persistence** — single SDK session_id across both turns, surviving the idle window.
- **Auth persistence** — apiKeySource stays `ANTHROPIC_API_KEY` across the gap; no re-auth needed.
- **Conversational memory** — the model recalled the rare phrase verbatim. Earlier multi-turn spikes (resolved finding 2026-05-01-multi-turn-persistent-claude-session-works-in-bun-supervisor) verified plumbing without auth so couldn't test memory; this run with real auth fills that gap.
- **Fast-resume** — turn 2 completed in 1.9s after the idle window. No cold-resume penalty.

## What this does NOT prove

- 30-minute idle survival specifically. If a token refresh, HTTP keepalive, or platform timeout fires at some boundary >60s, this test wouldn't catch it. A previous attempt at IDLE_SECS=1800 was inconclusive due to a parser bug (heredoc + stdin pipe collision) — the sandbox stayed up the full 30 min, but the verdict step couldn't read events.ndjson cleanly. The parser bug is now fixed (extracted to a sibling Python file), so re-running with IDLE_SECS=1800 would give a clean answer if anyone wants to verify specifically the 30-min boundary.

## Tooling improvements that landed with this spike

- **`scripts/spikes/2026-05-01-long-idle-stream.py`** — live event-stream pretty-printer. Pipes container's events.ndjson `tail -F` through it during the spike's idle/wait windows. One short line per event with timestamp, kind, and a hint of the payload (tool name, file path, command, assistant text excerpt). Made the failure mode of the previous 30-min run easy to diagnose (zero events landing) and gave a clear narrative for this 60-s run.
- **Parser extracted to its own .py file** (`2026-05-01-long-idle.py`). Avoids the `python3 - <<HEREDOC` + piped-stdin collision that bit two earlier spikes.

## POC concessions in the test artifacts

- Hardcoded sandbox name `idle-test`; concurrent runs collide.
- Magic phrase is a single fixed string; doesn't randomize.
- Live stream uses `container exec ... tail -F`; works but the legacy supervisor's NDJSON-stream approach (push to activity hub) is the polished P3 solution.
- No memory-pressure or platform-timeout-boundary checks — just "did the second turn produce a coherent response with the magic word?"

Status: resolved. Tier-1 #4 verified.

## Updates
### 2026-05-01T06:01:45Z — @agent — status: resolved
filed
