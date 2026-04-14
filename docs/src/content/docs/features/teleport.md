---
title: Teleport
description: Move an in-progress Claude Code conversation from one cspace container to another, preserving the transcript and git state.
sidebar:
  order: 1
---

import { Aside } from '@astrojs/starlight/components';

**Teleport** moves a live Claude Code session from one cspace container (the *source*) into a freshly provisioned container (the *target*), preserving the conversation transcript and the git state of the workspace. The source container stops but keeps its volumes, so you can inspect or discard it at your leisure. The target comes up idle and resume-ready — you reconnect and continue exactly where you left off, in a brand-new environment.

## How to use it

From inside the Claude Code conversation running in your source container, type:

```
/cspace-teleport <target-instance-name>
```

For example, if you're working inside `mercury` and want to continue in a fresh container called `venus`:

```
/cspace-teleport venus
```

Claude will run the teleport script, which bundles the workspace, stages the transcript, and invokes `cspace up venus --teleport-from <transfer-dir>` on the host (via docker-in-docker). When the target is up, the script stops the source and prints:

```
Teleport complete. Reconnect with: cspace resume venus
```

Exit the (now-stopped) source SSH session, and on the host run:

```
cspace resume venus
```

Claude Code resumes the same session id inside `venus`. Ask it to recall something from before the teleport — it remembers, because it's the same conversation.

<Aside type="note" title="Reconnect is manual">
cspace doesn't (yet) re-attach your terminal to the target automatically. The slash command runs *inside* the source container; it can't reach out to your host terminal and hand it off. After the source stops, run `cspace resume <target>` on the host yourself.
</Aside>

## When to use it

### Environment change

The original container is alive but wrong for the task:

- **Firewall is too tight.** The agent hits an external service that's not on the allowlist. Teleport to a new instance with `.cspace.json` firewall domains added.
- **Base image is wrong.** You want different tools, a different language runtime, or a different base image without losing the conversation.
- **Resource pressure.** The source is out of disk or memory; a fresh container gets you clean resources without restarting the reasoning.
- **The container is in a bad state.** Something in the dev environment has drifted (cached `node_modules`, orphaned processes, a broken lockfile); you want a clean slate but not a clean conversation.

### Recovery

The source container died or was killed (`docker rm`, accidental `cspace down`, host reboot), but its volumes are still on disk. Teleport from a sibling — actually, no: in the recovery case the source isn't running, so the slash command can't fire. This is a **v1 limitation**. For now, recovery means manually seeding a new instance from a previous snapshot if you have one; see "Limitations" below.

### Fork (emergent)

Not a primary use case, but falls out of the mechanism. If you want to try two different directions from the same point in a conversation, you *could* teleport `mercury` → `venus` *twice* at the same session point and end up with two target containers resuming the same transcript. Each continues independently from the fork point. Neither is the spec's intent — mostly useful when you realize halfway through a teleport that you want to keep the source around too (remove the `cspace stop` step by skipping it manually).

## When not to use it

- **You have uncommitted work you can't afford to lose.** Teleport uses `git bundle create --all`, which captures every branch and tag but **does not include the working tree or untracked files**. Commit (or stash-and-commit) before teleporting.
- **You just want a fresh container with the same repo.** That's `cspace up <name>` — no teleport needed.
- **The source is already dead.** Teleport is initiated from *inside* the source container's Claude session. If the source is gone, there's nothing to initiate from. (v1 limitation.)

## What travels

| Item | Travels? | Notes |
|---|---|---|
| Conversation transcript | ✅ | JSONL file, copied into target's `~/.claude/projects/-workspace/` |
| Session id | ✅ | SDK resumes by id; `SessionStart:resume` hook fires on target |
| Committed git history (all branches, tags) | ✅ | Via `git bundle create --all` |
| Origin remote URL | ✅ | Restored on the target so `git push` works |
| Uncommitted working-tree changes | ❌ | Commit before teleporting if you need them |
| Untracked files | ❌ | Same — commit them, or copy manually afterwards |
| `.env` files, secrets, browser cookies | ❌ | Target provisions these fresh |
| `node_modules`, `.venv`, build artifacts | ❌ | Regenerated on target |
| Supervisor inbox, prior coordinator directives | ❌ | Target starts with a clean supervisor |

## How it works (briefly)

1. The slash command expands to a Bash call to `teleport-prepare.sh` inside the source container.
2. The script finds the newest `.jsonl` under `$HOME/.claude/projects/-workspace/` — that's the active session transcript.
3. It runs `git bundle create --all` against `/workspace`, writes a `manifest.json`, and copies the transcript into the shared `/teleport/<session-id>/` directory (a host bind mount shared with every cspace container).
4. It invokes `cspace up <target> --teleport-from /teleport/<session-id>` via docker-in-docker. This is a variant of the normal `cspace up` path:
   - Workspace is seeded from the bundle instead of from the host repo.
   - Transcript is copied into the target's `~/.claude/projects/-workspace/<session-id>.jsonl`.
   - Supervisor launches with `ResumeSessionID`, passing `{ resume: <id> }` to the Claude Agent SDK's `query()`.
5. The source container is stopped (`cspace stop <source>`) but its volumes are preserved.

For full implementation details, see the design spec in the repo at `docs/superpowers/specs/2026-04-14-teleport-design.md`.

## Limitations

- **Recovery from a dead source is not supported in v1.** If the source container isn't running, the slash command can't fire. A host-side `cspace teleport` wrapper that works on stopped containers is tracked as a follow-up.
- **Agent-initiated teleport is not supported.** The agent can't ask to teleport itself (e.g., "this task needs a tool my firewall blocks; teleport me to a container with a wider allowlist"). Always user-initiated via the slash command.
- **Replace-in-place mode is not supported.** Teleport always creates a new named instance; it doesn't destroy-and-recreate the source under the same name. Use `cspace rm <source>` manually after you're done inspecting it.
- **Uncommitted work is lost.** By design — the bundle captures committed history, not working-tree state.

## Troubleshooting

- **`teleport: no live Claude session`** — The script couldn't find a `.jsonl` transcript. Either you haven't started a conversation yet (run at least one turn first), or `HOME` isn't set correctly inside the source container.
- **`teleport: cannot read $PROJECTS_DIR (permission denied)`** — Unusual; indicates `~/.claude/projects/-workspace/` has unexpected permissions. Inspect with `ls -la ~/.claude/projects/` inside the source.
- **`teleport manifest target ... does not match requested instance`** — Safety check tripped. You're pointing `cspace up --teleport-from` at a transfer dir that was prepared for a different target name. Either use the right name or remove the transfer dir and re-run the slash command.
- **Target comes up but doesn't resume** — Check `docker exec cs-<target> cat /tmp/agent-stderr.log` for supervisor errors. The target's transcript path is `~/.claude/projects/-workspace/<session-id>.jsonl`; confirm it's present.

## Related

- [`cspace up`](/cli-reference/instance-management/) — the underlying command teleport uses.
- [`cspace stop`](/cli-reference/instance-management/) — the non-destructive stop that leaves the source inspectable.
- [`cspace resume`](/cli-reference/instance-management/) — reconnect to the target after teleporting.
