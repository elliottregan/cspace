"""Companion parser for 2026-05-01-multi-turn-persistence.sh.

Reads NDJSON event log on stdin, prints a verdict JSON on stdout.

POC concession: lives next to the bash script rather than being a Go test
or a real assertion library. Polished version belongs in
internal/cli/cmd_cspace2_integration_test.go (P1 Task 9).
"""

import json
import sys


def main() -> int:
    turns = 0
    inits = 0
    assistants = 0
    results = 0
    errors = 0
    session_ids = set()

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        e = json.loads(line)
        kind = e.get("kind")
        data = e.get("data", {}) or {}
        if kind == "user-turn":
            turns += 1
        elif kind == "sdk-event":
            sid = data.get("session_id")
            if sid:
                session_ids.add(sid)
            t = data.get("type")
            if t == "system" and data.get("subtype") == "init":
                inits += 1
            elif t == "assistant":
                assistants += 1
            elif t == "result":
                results += 1
        elif kind == "sdk-error":
            errors += 1

    print(json.dumps({
        "user_turns": turns,
        "sdk_inits": inits,
        "sdk_assistants": assistants,
        "sdk_results": results,
        "sdk_errors": errors,
        "distinct_session_ids": sorted(session_ids),
    }, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
