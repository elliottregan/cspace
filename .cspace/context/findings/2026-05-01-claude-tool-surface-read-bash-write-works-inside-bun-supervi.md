---
title: Claude tool surface (Read/Bash/Write) works inside Bun-supervised sandbox
date: 2026-05-01
kind: finding
status: resolved
category: observation
tags: verification, tools, supervisor, permissions, p0-extension, p1-blocker-fixed
related: scripts/spikes/2026-05-01-tool-use.sh, scripts/spikes/2026-05-01-tool-use.py, lib/agent-supervisor-bun/src/claude-runner.ts, lib/templates/Dockerfile.prototype
---

## Summary
Open question for P1 review: when Claude is running inside a cspace sandbox via the Bun supervisor, does the agent actually have a working tool surface? P0's previous spikes only verified plumbing (turns land, session is persistent, auth works). Tool USE was untested. Spike PASSes after fixing two real prerequisites: (1) Bun supervisor was missing `permissionMode: "bypassPermissions"` in its `query()` options — without it, unattended tool calls hang on permission prompts; (2) Claude Code refuses bypassPermissions when running as root, but Dockerfile.prototype was running everything as root. Added a `dev` user (uid 1000) and switched to non-root by default. After both fixes: Read, Bash, and Write tools all fire, the agent's Write side-effect is visible on the host filesystem via the bind mount.

## Details
## Test method

`scripts/spikes/2026-05-01-tool-use.sh` builds on the workspace-clone design and sends three turns into one persistent session, each requiring a different tool:

1. Read tool — "Read /workspace/CLAUDE.md and tell me the very first heading verbatim."
2. Bash tool — "Run the bash command 'ls /workspace | wc -l' and report only the resulting number."
3. Write tool — "Use the Write tool to create the file /workspace/touched-by-agent.txt with content: <probe>"

The companion parser (`2026-05-01-tool-use.py`) inspects events.ndjson for tool_use blocks of each name, asserts no SDK errors / API retries / result errors, and the bash script additionally verifies the Write side-effect is visible on the host filesystem at the bind-mount path.

## Result

```
read_ok: true   (input.file_path = /workspace/CLAUDE.md)
bash_ok: true   (input.command contained /workspace)
write_ok: true  (input.content contained the probe phrase)
api_retry_count: 0
sdk_error_count: 0
result_is_error_values: [false, false, false]
PASS

Host filesystem after run:
  ~/.cspace/clones/cspace/tool-test/touched-by-agent.txt
  contents: cspace-tool-spike-1777611751
```

The agent's Write tool actually wrote the file. Host sees it instantly via the bind mount. Confirmed end-to-end: Bun supervisor → Claude SDK → tool registration → real tool invocation → real side-effect on host.

## Two prerequisite fixes baked into this finding

### 1. Bun supervisor sets `permissionMode: "bypassPermissions"`

`lib/agent-supervisor-bun/src/claude-runner.ts` previously called `query()` without setting permissionMode. The SDK default is `"default"` which prompts for confirmation on every tool call. Sandboxes are unattended; no human is there to approve. The existing Node supervisor at `lib/agent-supervisor/args.mjs` has set `bypassPermissions` since day one — the new Bun supervisor was just missing that option.

### 2. Sandbox runs as non-root user `dev`

Claude Code refuses `--dangerously-skip-permissions` (the underlying mechanism for `bypassPermissions`) with the error:
```
--dangerously-skip-permissions cannot be used with root/sudo privileges for security reasons
```

`Dockerfile.prototype` previously had no `USER` directive, so everything ran as root. Updated to:
- Create `dev` user (uid 1000) with NOPASSWD sudo
- Install Bun and Claude Code as `dev` so binaries land in `/home/dev/.bun/`
- Re-symlink `/usr/local/bin/{bun,claude}` for stable PATH
- `chown dev:dev /workspace /sessions`
- `USER dev` at end of Dockerfile

Mirrors the legacy `Dockerfile`'s pattern (`adduser dev wheel` + `USER dev`).

## What this proves

- Tool registration in the SDK reaches Claude Code's CLI inside the container.
- bypassPermissions correctly disables the interactive permission gate when running as a non-root user.
- The bind-mounted clone (per the workspace-mount design) actually receives writes from the agent, instantly, no sync step.
- The default tool surface (Read/Bash/Write) is fully functional. By extension, peer tools using the same registration plumbing — Edit, Glob, Grep, NotebookEdit, etc. — should work; not individually tested but presumed correct.

## What this does NOT prove

- WebFetch / WebSearch / external-API tools (require DNS + outbound HTTPS to non-anthropic.com hosts; assume similar coverage but not specifically tested).
- Long tool chains in a single turn (e.g. agent doing 10 Bash calls in sequence) — should work given multi-turn already works, but worth a stress test.
- MCP server registration (e.g. cspace-context, github MCP). Default tools work; custom MCP servers are a P2+ concern.
- That tool errors are surfaced cleanly to the agent (e.g. when a Bash command exits non-zero, does the agent see the failure?). Probably yes; not tested.

## Implications for P1

- `lib/agent-supervisor-bun/src/claude-runner.ts`'s `permissionMode: "bypassPermissions"` change carries forward.
- `lib/templates/Dockerfile.prototype`'s non-root-user pattern carries forward into `Dockerfile.cspace2`. P1 should also verify other in-container scripts (entrypoint, init scripts) work as the dev user.
- `chown` of /workspace and /sessions to `dev:dev` is required at image build time. If P1 introduces other host-bind-mounted paths, they need the same treatment.
- The existing Dockerfile pattern mounts `~/.claude` from the host into `/home/dev/.claude` to share auth state between host and sandbox. P1 may want to do the same — or rely entirely on the env-var alias path we already proved (CLAUDE_CODE_OAUTH_TOKEN → ANTHROPIC_API_KEY).

## POC concessions

- `permissionMode: "bypassPermissions"` is correct for autonomous sandboxes but inappropriate for interactive ones (like the future mercury-style sandbox, where a human pairs with the agent and might want to approve actions). P1 should make this configurable per sandbox role.
- The dev user has NOPASSWD sudo. That's fine for a single-user dev sandbox but should be reviewed before any multi-tenant scenario.
- No verification of tool-error propagation; agent gets clean tool results in the happy path only.

Status: resolved. Tool surface is verified for P1 design.

## Updates
### 2026-05-01T05:04:08Z — @agent — status: resolved
filed
