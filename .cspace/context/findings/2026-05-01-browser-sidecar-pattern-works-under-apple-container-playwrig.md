---
title: Browser sidecar pattern works under Apple Container; Playwright MCP drives Chrome via CDP
date: 2026-05-01
kind: finding
status: resolved
category: observation
tags: verification, browser, playwright, mcp, sidecar, p1, topology
related: scripts/spikes/2026-05-01-browser-sidecar.sh, scripts/spikes/2026-05-01-browser-sidecar.py, lib/agent-supervisor-bun/src/claude-runner.ts, lib/templates/Dockerfile.prototype, internal/cli/cmd_prototype_up.go
---

## Summary
Open question for the sandbox-architecture spec: can the legacy "browser sidecar" pattern (separate container running Chrome with CDP exposed) work under Apple Container's default bridge networking? Verified PASS. Two containers per sandbox — cspace2 (lean, no Chromium) and a Playwright base image running headless Chromium with socat bridging 0.0.0.0:9222 → 127.0.0.1:9223. The supervisor's playwright-mcp inside the sandbox connects via `--cdp-endpoint http://<sidecar-ip>:9222`. Agent successfully called mcp__playwright__browser_navigate, the navigation reached the sidecar's Chrome, and the agent got back the real DOM title from https://example.com.

## Details
## Test method

`scripts/spikes/2026-05-01-browser-sidecar.sh`:

1. Start the sidecar container `cspace-<project>-<sandbox>-browser` from `mcr.microsoft.com/playwright:v1.58.0-noble`. Use `--dns 1.1.1.1 --dns 8.8.8.8` so apt-get can install socat (the sidecar isn't a cspace2 container so it doesn't inherit our DNS injection by default).
2. Sidecar entrypoint: install socat, launch Chromium on 127.0.0.1:9223, wait for CDP to bind, then `socat TCP-LISTEN:9222,fork,reuseaddr TCP:127.0.0.1:9223` to expose CDP on 0.0.0.0:9222.
3. Capture sidecar IP via `container inspect`. Wait for the host to be able to reach `http://<ip>:9222/json/version`.
4. Bring up the cspace2 sandbox with `--env CSPACE_BROWSER_CDP_URL=http://<ip>:9222` (new CLI flag added to prototype-up).
5. Supervisor reads `CSPACE_BROWSER_CDP_URL` from process env when constructing `mcpServers` for `query()`. Passes `--cdp-endpoint <url>` to playwright-mcp; passes `--browser-url <url>` to chrome-devtools-mcp.
6. Send the agent: "Use the Playwright MCP browser_navigate tool to navigate to https://example.com, then report the exact <title> text."
7. Inspect events.ndjson for tool_use(name="mcp__playwright__*") and assistant text containing "Example Domain".

## Result

```json
{
  "playwright_tools": [
    {"name": "mcp__playwright__browser_navigate", "input_keys": ["url"]}
  ],
  "playwright_tool_calls": 1,
  "final_assistant_text": "Example Domain",
  "title_seen": true,
  "sdk_error_count": 0,
  "result_is_error_values": [false],
  "no_errors": true,
  "PASS": true
}
```

End-to-end CDP path round-trips correctly. The agent observed real DOM content driven by tools the supervisor registered.

## Topology that works

```
Host (Apple Container default bridge 192.168.64.0/24)
├── cspace-cspace-browser-test            (sandbox, lean image)
│   ├── cspace-supervisor (Bun)
│   └── playwright-mcp (stdio child of Claude SDK)
│       └── --cdp-endpoint http://192.168.64.67:9222 ──┐
│                                                      │
└── cspace-cspace-browser-test-browser                 │ CDP/JSON-RPC
    (192.168.64.67)                                    │
    ├── socat TCP-LISTEN:9222 → 127.0.0.1:9223 ◄───────┘
    └── chromium --remote-debugging-port=9223 (headless)
```

## Implementation pieces (carry forward into P1)

1. **Image stays lean.** Dockerfile.prototype `npm install -g @playwright/mcp@0.0.72 chrome-devtools-mcp@0.23.0` only — no Chromium baked in. Image stays ~300MB instead of ~1GB.

2. **MCP server registration via env.** `lib/agent-supervisor-bun/src/claude-runner.ts` reads `process.env.CSPACE_BROWSER_CDP_URL` at supervisor boot. If set, registers playwright-mcp with `--cdp-endpoint` and chrome-devtools-mcp with `--browser-url`. If unset, registers them with no args (tool calls will fail at runtime but supervisor doesn't crash).

3. **`prototype-up` `--env KEY=VALUE` flag.** Added (`internal/cli/cmd_prototype_up.go`) so spikes can inject arbitrary env vars without polluting `.cspace/secrets.env`. P1's cspace2-up will absorb the browser sidecar lifecycle and inject CSPACE_BROWSER_CDP_URL automatically.

4. **DNS for the sidecar.** The sidecar isn't a cspace2 container so Apple Container's broken default DNS bites it on `apt-get update`. Pass `--dns 1.1.1.1 --dns 8.8.8.8` at sidecar `container run` time. Same fix as cspace2's substrate adapter, just applied per-container by whatever spawns the sidecar.

5. **socat workaround for Chromium's address binding.** Modern Chromium ignores `--remote-debugging-address=0.0.0.0` and force-binds to 127.0.0.1. The legacy compose stack handles this via socat; we mirror it. Documented inline in the spike script.

## P1 work this unblocks

When P1's cspace2-up subsumes browser sidecar lifecycle, the sequence is:

```
cspace2-up <name> [--browser]
  1. start sidecar container if --browser flag set
  2. wait for CDP endpoint
  3. capture sidecar IP
  4. start sandbox container with CSPACE_BROWSER_CDP_URL injected
  5. register both in sandbox-registry so cspace2-down knows to stop both
```

Concrete additions:
- `internal/cli/cmd_cspace2_up.go` — `--browser` flag (or always-on for interactive sandboxes).
- `internal/registry/registry.go` — Entry gains optional `BrowserContainerName` field for teardown coordination.
- `internal/cli/cmd_cspace2_down.go` — stop browser sidecar after sandbox.
- Sidecar image — for now, `mcr.microsoft.com/playwright:v1.58.0-noble`. P2/P3 may build a custom lean image (Chromium + socat only, ~400MB).

## POC concessions

- `mcr.microsoft.com/playwright:v1.58.0-noble` (~2.5 GB unpacked) is bigger than necessary for a Chrome+socat sidecar. Reasonable starting point; build lean later if disk pressure matters.
- socat workaround masks Chromium's binding restriction. If Chromium ever changes behavior, revisit.
- Wildcard glob `/ms-playwright/chromium-*/chrome-linux/chrome` would break if Microsoft restructures their image layout. Tracked.
- Browser sidecar restarts aren't handled. If Chrome OOMs, the sandbox's MCP servers fail until the user manually restarts the sidecar. P1 should add `cspace2-restart-browser <sandbox>`.
- No per-session profile isolation (Chrome uses /tmp). P2 work.
- Sidecar IP is captured at sandbox-start time and stored in env. If Apple Container ever supports static container IPs, this becomes simpler.

Status: resolved. Sidecar topology validated; ready for P1 to absorb the lifecycle.

## Updates
### 2026-05-01T05:44:57Z — @agent — status: resolved
filed
