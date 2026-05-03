"""Companion parser for 2026-05-01-browser-sidecar.sh.

Reads events.ndjson on stdin. Verdict (exit 0 if all pass):
  - At least one tool_use whose name starts with "mcp__playwright__"
    or is a Playwright-shaped browser tool (browser_navigate, etc.)
  - The final assistant text contains "Example Domain" (the title of
    https://example.com — proof the CDP round-trip actually fetched DOM)
  - No SDK errors / non-success results.
"""

import json
import sys


def main() -> int:
    playwright_tools = []
    last_text = None
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
        d = e.get("data", {}) or {}
        t = d.get("type")
        if t == "assistant":
            for c in (d.get("message", {}) or {}).get("content", []) or []:
                if c.get("type") == "tool_use":
                    name = c.get("name", "")
                    # Match playwright-mcp tool names. SDK exposes them as
                    # `mcp__playwright__<tool>` after MCP registration.
                    if name.startswith("mcp__playwright__") or name.startswith("browser_"):
                        playwright_tools.append({
                            "name": name,
                            "input_keys": list((c.get("input") or {}).keys()),
                        })
                elif c.get("type") == "text":
                    last_text = c.get("text", "")
        elif t == "result":
            result_errors.append(bool(d.get("is_error", False)))

    title_seen = "Example Domain" in (last_text or "")
    no_errors = sdk_errors == 0 and ((not result_errors) or all(not e for e in result_errors))

    verdict = {
        "playwright_tools": playwright_tools,
        "playwright_tool_calls": len(playwright_tools),
        "final_assistant_text": (last_text or "")[:200],
        "title_seen": title_seen,
        "sdk_error_count": sdk_errors,
        "result_is_error_values": result_errors,
        "no_errors": no_errors,
        "PASS": len(playwright_tools) > 0 and title_seen and no_errors,
    }
    print(json.dumps(verdict, indent=2))
    return 0 if verdict["PASS"] else 1


if __name__ == "__main__":
    sys.exit(main())
