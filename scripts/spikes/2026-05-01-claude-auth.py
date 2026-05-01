"""Companion parser for 2026-05-01-claude-auth.sh.

Reads NDJSON event log on stdin. Argv[1] is the expected probe phrase.

Verdict (exit 0 if all pass, exit 1 otherwise):
  - apiKeySource in init event is not "none"
  - At least one assistant text mentions the probe phrase
  - result event has is_error == False (or absent)

POC concession: lives next to the bash script. Polished version in
internal/cli/cmd_cspace2_integration_test.go.
"""

import json
import sys


def main() -> int:
    probe = sys.argv[1] if len(sys.argv) > 1 else "CSPACE-AUTH-OK"

    api_key_sources = []
    assistant_texts = []
    result_errors = []
    raw_lines = 0

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        raw_lines += 1
        e = json.loads(line)
        if e.get("kind") != "sdk-event":
            continue
        data = e.get("data", {}) or {}
        t = data.get("type")
        if t == "system" and data.get("subtype") == "init":
            api_key_sources.append(data.get("apiKeySource"))
        elif t == "assistant":
            msg = data.get("message", {}) or {}
            for block in msg.get("content", []) or []:
                if block.get("type") == "text":
                    assistant_texts.append(block.get("text", ""))
        elif t == "result":
            result_errors.append(bool(data.get("is_error", False)))

    auth_ok = bool(api_key_sources) and all(s and s != "none" for s in api_key_sources)
    probe_seen = any(probe in txt for txt in assistant_texts)
    no_errors = (not result_errors) or all(not err for err in result_errors)

    verdict = {
        "raw_lines": raw_lines,
        "apiKeySource_values": api_key_sources,
        "auth_ok": auth_ok,
        "assistant_text_excerpts": [t[:120] for t in assistant_texts],
        "probe_seen": probe_seen,
        "result_is_error_values": result_errors,
        "no_errors": no_errors,
        "PASS": auth_ok and probe_seen and no_errors,
    }
    print(json.dumps(verdict, indent=2))

    return 0 if verdict["PASS"] else 1


if __name__ == "__main__":
    sys.exit(main())
