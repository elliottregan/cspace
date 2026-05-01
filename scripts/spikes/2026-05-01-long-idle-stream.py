"""Live event-stream pretty-printer for 2026-05-01-long-idle.sh.

Reads NDJSON on stdin one line at a time, emits a single short summary
line per event with [ev <ts>] prefix so it interleaves nicely with the
script's own status messages.

POC concession: minimal formatter; full stream-shim equivalents (token
counts, tool-call inputs, etc.) live in the polished P1 supervisor work.
"""

import json
import sys


def short(evt: dict) -> str:
    kind = evt.get("kind")
    data = evt.get("data", {}) or {}

    if kind == "supervisor-start":
        return f"supervisor-start port={data.get('port')} session={data.get('session')}"
    if kind == "user-turn":
        text = (data.get("text") or "")[:80]
        return f"user-turn  source={data.get('source')} text={text!r}"
    if kind == "sdk-error":
        return f"sdk-error  {(data.get('error') or '')[:120]}"
    if kind != "sdk-event":
        return f"{kind}  {str(data)[:120]}"

    t = data.get("type")
    st = data.get("subtype")
    if t == "system" and st == "init":
        return f"sdk init   apiKeySource={data.get('apiKeySource')} model={data.get('model')} sid={(data.get('session_id') or '')[:8]}"
    if t == "system" and st == "api_retry":
        return f"sdk retry  attempt={data.get('attempt')} delay_ms={int(data.get('retry_delay_ms') or 0)}"
    if t == "assistant":
        msg = data.get("message", {}) or {}
        for c in msg.get("content", []) or []:
            ct = c.get("type")
            if ct == "tool_use":
                inp = c.get("input", {}) or {}
                hint = inp.get("command") or inp.get("file_path") or inp.get("url") or inp.get("pattern") or ""
                return f"tool_use   {c.get('name')}  {str(hint)[:80]}"
            if ct == "text":
                return f"assistant  {(c.get('text') or '')[:120]!r}"
        return f"assistant  (empty content)"
    if t == "user":
        # Tool results show up as user-typed events from the SDK's perspective.
        msg = data.get("message", {}) or {}
        for c in msg.get("content", []) or []:
            if c.get("type") == "tool_result":
                content = c.get("content")
                if isinstance(content, list):
                    parts = [p.get("text", "") for p in content if p.get("type") == "text"]
                    text = " | ".join(parts)
                else:
                    text = str(content)
                tail = text.replace("\n", " ⏎ ")[-160:]
                return f"tool_result {tail!r}"
        return f"sdk user   (no tool_result block)"
    if t == "result":
        return f"sdk result is_error={data.get('is_error')} duration_ms={data.get('duration_ms')} num_turns={data.get('num_turns')}"
    return f"sdk-event  type={t} subtype={st}"


def main() -> int:
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            evt = json.loads(line)
        except json.JSONDecodeError:
            continue
        ts = (evt.get("ts") or "").split("T")[-1].split(".")[0]
        try:
            print(f"[ev {ts}] {short(evt)}", flush=True)
        except BrokenPipeError:
            return 0
    return 0


if __name__ == "__main__":
    sys.exit(main())
