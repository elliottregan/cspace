"""Companion parser for 2026-05-01-tool-use.sh.

Reads events.ndjson on stdin. Argv[1] is the Write-tool probe phrase the
agent was asked to write to disk.

Verdict (exit 0 if all three tool checks pass, exit 1 otherwise):
  - At least one tool_use(name=Read) event with input.file_path under /workspace
  - At least one tool_use(name=Bash) event whose input.command involves /workspace
  - At least one tool_use(name=Write) event whose input.content contains the probe
  - No SDK errors on any turn
"""

import json
import sys


def main() -> int:
    probe = sys.argv[1] if len(sys.argv) > 1 else "cspace-tool-spike"

    read_uses = []
    bash_uses = []
    write_uses = []
    api_errors = 0
    sdk_errors = 0
    result_errors = []

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        e = json.loads(line)
        kind = e.get("kind")
        if kind == "sdk-error":
            sdk_errors += 1
            continue
        if kind != "sdk-event":
            continue
        data = e.get("data", {}) or {}
        t = data.get("type")
        if t == "system" and data.get("subtype") == "api_retry":
            api_errors += 1
        elif t == "result":
            result_errors.append(bool(data.get("is_error", False)))
        elif t == "assistant":
            msg = data.get("message", {}) or {}
            for block in msg.get("content", []) or []:
                if block.get("type") != "tool_use":
                    continue
                name = block.get("name", "")
                inp = block.get("input", {}) or {}
                summary = {"name": name, "input": inp}
                if name == "Read":
                    read_uses.append(summary)
                elif name == "Bash":
                    bash_uses.append(summary)
                elif name == "Write":
                    write_uses.append(summary)

    # Predicates for each tool.
    read_ok = any(
        str(u.get("input", {}).get("file_path", "")).startswith("/workspace")
        for u in read_uses
    )
    bash_ok = any(
        "/workspace" in str(u.get("input", {}).get("command", ""))
        for u in bash_uses
    )
    write_ok = any(
        probe in str(u.get("input", {}).get("content", ""))
        for u in write_uses
    )
    no_errors = (sdk_errors == 0) and ((not result_errors) or all(not err for err in result_errors))

    verdict = {
        "read_uses": read_uses,
        "bash_uses": bash_uses,
        "write_uses": write_uses,
        "read_ok": read_ok,
        "bash_ok": bash_ok,
        "write_ok": write_ok,
        "api_retry_count": api_errors,
        "sdk_error_count": sdk_errors,
        "result_is_error_values": result_errors,
        "no_errors": no_errors,
        "PASS": read_ok and bash_ok and write_ok and no_errors,
    }
    print(json.dumps(verdict, indent=2))

    return 0 if verdict["PASS"] else 1


if __name__ == "__main__":
    sys.exit(main())
