---
title: Supervisor Commands
description: Host-side commands for interacting with running agent sessions.
sidebar:
  order: 4
---

Host-side commands for interacting with running agent sessions. All communication goes through Unix sockets on the shared logs volume — delivery is instant.

## `cspace send`

Send a message to a running agent or coordinator.

### Syntax

```bash
cspace send <instance> "text"
cspace send _coordinator "text"
```

### Description

Injects a user turn into the target's live conversation via the supervisor socket. The message appears immediately as if the user typed it.

Use `_coordinator` as the target to reach the coordinator — this is the well-known address that all workers use to report completion. Use an instance name (e.g., `mercury`, `issue-42`) to send to a specific agent.

:::note
One-shot agents (`cspace up <name> --prompt "…"` without `--persistent`) exit after their first result, so a `send` after that does nothing. For back-and-forth, start the agent with `--persistent` — see [Persistent Agents](/cli-reference/autonomous-agents/#persistent-agents-with-cspace-up---persistent).
:::

### Examples

```bash
# Send a directive to an agent
cspace send mercury "Focus on the authentication module first"

# Report completion to the coordinator (used by workers in their final step)
cspace send _coordinator "Worker issue-42 complete. Status: success. PR: https://github.com/.../pull/99"

# Redirect the coordinator
cspace send _coordinator "Skip issue #15, it's already been resolved"
```

---

## `cspace interrupt`

Interrupt a running agent session.

### Syntax

```bash
cspace interrupt <instance>
```

### Description

Sends an interrupt signal to the specified instance's supervisor. The supervisor exits cleanly, saving state. The workspace is preserved — all files, branches, and uncommitted changes remain intact.

### Examples

```bash
cspace interrupt mercury
cspace interrupt _coordinator
```

---

## `cspace agent-status`

Show supervisor status as JSON.

### Syntax

```bash
cspace agent-status <instance>
```

### Description

Returns raw JSON status from the instance's supervisor socket, including role, session ID, turn count, and last activity.

### Examples

```bash
cspace agent-status mercury
cspace agent-status mercury | jq .
```

---

## `cspace restart-supervisor`

Restart the agent supervisor while preserving the workspace.

### Syntax

```bash
cspace restart-supervisor <instance> [--reason "text"]
```

### Flags

| Flag | Description |
|------|-------------|
| `--reason "text"` | Explain why the restart is needed (visible to the agent when it resumes) |

### Description

Restarts an agent's supervisor inside its existing container:

1. Sends an interrupt to the old supervisor via the socket
2. Waits up to 30 seconds for the old supervisor to exit (detected by socket disappearing)
3. If `--reason` is given, prepends a restart context marker to the prompt
4. Launches a new supervisor in detached mode with the same prompt file

**Preserved:** all files, branches, uncommitted changes, the original prompt.
**Must be re-established by the agent:** browser sessions, running servers, external state.

### Examples

```bash
cspace restart-supervisor mercury
cspace restart-supervisor mercury --reason "Agent stuck on a dead Playwright connection"
```
