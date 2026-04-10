---
title: Supervisor Commands
description: Host-side commands for interacting with running agent sessions.
sidebar:
  order: 4
---

Host-side dispatch commands for interacting with running agent sessions. All supervisor commands communicate with the agent-supervisor process inside a running cspace container via a shared logs volume and Unix socket.

## `cspace send`

Inject a user message into a running agent session.

### Syntax

```bash
cspace send <instance> "text"
```

### Description

Sends a message to the specified instance's supervisor, which injects it as a user turn in the agent's conversation. Useful for providing additional context, redirecting the agent, or giving instructions mid-run.

### Examples

```bash
cspace send mercury "Focus on the authentication module first"
cspace send coord-1234 "Skip issue #15, it's already been resolved"
```

---

## `cspace respond`

Reply to a pending agent question.

### Syntax

```bash
cspace respond <instance> <question-id> "text"
```

### Parameters

| Parameter | Description |
|-----------|-------------|
| `<instance>` | The target instance name |
| `<question-id>` | The question ID (from `cspace ask`) |
| `"text"` | Your answer |

### Description

Answers a question that an agent has asked via its structured inbox. Use `cspace ask` to see pending questions and their IDs.

### Examples

```bash
cspace respond mercury q-abc123 "Yes, use the existing database schema"
cspace respond venus q-def456 "The API endpoint is /api/v2/users"
```

---

## `cspace ask`

List pending agent questions.

### Syntax

```bash
cspace ask [instance]
```

### Description

Displays a table of pending questions from running agents. Without an instance name, shows questions across all instances. With an instance name, shows only questions from that specific agent.

The output includes question IDs needed for `cspace respond`.

### Examples

```bash
# List all pending questions across all instances
cspace ask

# List pending questions for a specific instance
cspace ask mercury
```

---

## `cspace watch`

Stream agent notifications and questions in real-time.

### Syntax

```bash
cspace watch [instance]
```

### Description

Opens an interactive stream that displays agent status events and question prompts as they occur. Without an instance name, watches all instances. With an instance name, watches only that specific agent.

This is an interactive command that keeps the terminal open — use Ctrl+C to exit.

### Examples

```bash
# Watch all instances
cspace watch

# Watch a specific instance
cspace watch mercury
```

---

## `cspace interrupt`

Interrupt a running agent session.

### Syntax

```bash
cspace interrupt <instance>
```

### Description

Sends an interrupt signal to the specified instance's supervisor. The supervisor exits cleanly, saving state. The workspace state is preserved — all files, branches, and uncommitted changes remain intact.

### Examples

```bash
cspace interrupt mercury
```

---

## `cspace agent-status`

Show supervisor status as JSON.

### Syntax

```bash
cspace agent-status <instance>
```

### Description

Returns raw JSON status from the instance's supervisor, including session state and current task information.

### Examples

```bash
cspace agent-status mercury

# Pipe to jq for pretty-printing
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

Restarts an agent's supervisor inside its existing container. The process:

1. Sends an interrupt to the old supervisor via the socket
2. Waits up to 30 seconds for the old supervisor to exit cleanly
3. If `--reason` is given, prepends a restart context marker to the prompt so the agent understands why it was restarted
4. Launches a new supervisor in detached mode with the same prompt file
5. Sets an inbox filter to ignore messages older than the restart timestamp

**What is preserved:**
- All files, branches, and uncommitted changes in the workspace
- The original prompt file

**What must be re-established by the agent:**
- Browser sessions
- Running test servers
- Any other external state

### Examples

```bash
# Simple restart
cspace restart-supervisor mercury

# Restart with a reason
cspace restart-supervisor mercury --reason "Agent was stuck in a loop"
cspace restart-supervisor venus --reason "Need to pick up new environment variables"
```
