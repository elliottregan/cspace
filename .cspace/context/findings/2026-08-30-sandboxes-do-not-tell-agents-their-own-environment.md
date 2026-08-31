---
title: sandboxes don't tell agents their own environment, so every project documents it (and drifts)
date: 2026-08-30
kind: finding
status: open
category: refactor
tags: sandbox, mcp, browser, docs, agent-ux, design
---

## Summary
Nothing in a sandbox tells an agent how to reach the workspace or what the
browser tools are called. Every project has to write it down, and the write-up
drifts. resume-redux's CLAUDE.md told agents to navigate by `$(hostname)` —
which the browser sidecar cannot resolve — and to use `mcp__playwright__*`
tools, which exist in no sandbox. Both had been wrong for months; an agent in
venus found them by hitting the failures.

The facts an agent needs are all known to cspace at boot. It just never says
them.

## Details
Two pieces of environment are guessable-wrong today:

**Workspace host.** `hostname` returns the container name
(`cspace-resume-redux-venus`), which resolves only inside that sandbox. The
browser sidecar is a separate microVM with its own network namespace and
resolves `$CSPACE_WORKSPACE_HOST` (`<sandbox>.<project>.cspace.test`).
`docs/env-cspace.md` has always said this; a project doc said the opposite, and
the project doc is what the agent read.

**Browser MCP prefix depends on session type**, because cspace registers the
same two servers twice by different mechanisms:

| Session | Registered by | Tools |
|---|---|---|
| `cspace attach` | the `cspace-browser` plugin's `.mcp.json` | `mcp__plugin_cspace-browser_cspace-playwright__*` |
| `cspace send` (supervisor) | `claude-runner.ts`, since the Agent SDK does not auto-load CLI plugin MCP servers | `mcp__cspace-playwright__*` |

So any project instruction naming a single prefix is wrong in half of its
sessions. This is cspace's own asymmetry leaking into every project's docs.

## Proposed shape
Have the environment describe itself, so the answer comes from the sandbox
rather than from documentation that drifts. Options, none chosen:

1. **Seed user-scope memory in the sandbox.** The entrypoint writes
   `~/.claude/CLAUDE.md` with the generated facts (workspace host, CDP
   endpoint, the tool prefix for *this* session type, ports). Outside
   `/workspace`, so it never pollutes the project's git. Caveat: the
   supervisor runs with `settingSources: ["project"]`, so user-scope memory may
   not reach headless sessions — those would need the same facts appended to
   the role/system prompt instead, which is a second mechanism to keep in sync.
2. **A command agents can ask.** `cspace env` (in-sandbox) prints the same
   facts. Nothing to seed or keep fresh, but it only helps agents that know to
   run it — which is the discovery problem again, one level up.
3. **Normalize the prefixes instead.** Register the browser servers the same
   way on both paths so there is one name to document. Smaller surface, and it
   removes the asymmetry rather than describing it — but it means either
   teaching the supervisor to load plugin MCP servers or dropping the plugin
   registration for attach sessions.

(3) is worth weighing before (1) or (2): documenting a confusing thing is more
work forever than making it unconfusing once.

## Updates
### 2026-08-30 — status: open
Filed after the venus report. Interim: cspace's CLAUDE.md now documents both
prefixes and the workspace-host rule, and resume-redux's CLAUDE.md was
corrected — but that is the same "write it down in every project" pattern that
produced the drift.
