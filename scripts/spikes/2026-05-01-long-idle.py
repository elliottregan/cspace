"""Companion parser for 2026-05-01-long-idle.sh.

Reads events.ndjson on stdin. Argv[1] is the magic phrase the agent
was asked to remember in turn 1.

Verdict (exit 0 if all pass):
  - At least 2 user-turn events (turn 1 + turn 2).
  - Exactly 1 distinct session_id (session was persistent across idle).
  - sdk_errors == 0.
  - Final result.is_error == False.
  - Turn-2 assistant text contains the magic phrase (memory survived idle).
"""

import json
import sys


def main() -> int:
    magic = sys.argv[1] if len(sys.argv) > 1 else "(no-magic)"

    user_turns = 0
    distinct_session_ids = set()
    sdk_errors = 0
    last_result = None
    assistant_texts = []  # (turn_index_at_emission, text)

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        e = json.loads(line)
        k = e.get("kind")
        if k == "user-turn":
            user_turns += 1
        elif k == "sdk-error":
            sdk_errors += 1
        elif k == "sdk-event":
            d = e.get("data", {}) or {}
            sid = d.get("session_id")
            if sid:
                distinct_session_ids.add(sid)
            t = d.get("type")
            if t == "assistant":
                for c in (d.get("message", {}) or {}).get("content", []) or []:
                    if c.get("type") == "text":
                        assistant_texts.append((user_turns, c.get("text", "")))
            elif t == "result":
                last_result = d

    turn2_assistant = [t for n, t in assistant_texts if n >= 2]
    magic_in_turn2 = any(magic in t for t in turn2_assistant)

    verdict = {
        "user_turns": user_turns,
        "distinct_session_ids": sorted(distinct_session_ids),
        "sdk_errors": sdk_errors,
        "last_result_is_error": (last_result or {}).get("is_error"),
        "turn2_assistant_excerpt": (turn2_assistant[-1][:200] if turn2_assistant else None),
        "magic_word_recalled": magic_in_turn2,
        "PASS": (
            user_turns >= 2
            and len(distinct_session_ids) == 1
            and sdk_errors == 0
            and (last_result or {}).get("is_error") is False
            and magic_in_turn2
        ),
    }
    print(json.dumps(verdict, indent=2))
    return 0 if verdict["PASS"] else 1


if __name__ == "__main__":
    sys.exit(main())
