---
title: Instance Management
description: Commands for creating, connecting to, listing, and destroying cspace devcontainer instances.
sidebar:
  order: 2
---

Commands for creating, listing, and destroying cspace devcontainer instances.

## `cspace up`

Create or reconnect to an instance and launch Claude Code.

### Syntax

```bash
cspace up [name|branch] [flags]
```

### Flags

| Flag | Description |
|------|-------------|
| `--no-claude` | Create the instance without launching Claude Code |
| `--prompt "text"` | Run as a one-shot autonomous agent with the given inline prompt |
| `--prompt-file <path>` | Run as a one-shot autonomous agent with the prompt loaded from a file |
| `--base <branch>` | Override which branch is checked out in the container (the instance name is still derived from the positional argument) |

`--prompt` and `--prompt-file` are mutually exclusive.

### Description

`cspace up` provisions a devcontainer instance and, by default, drops the user into a live interactive Claude Code session.

**Instance naming:**
- If no name is given, an auto-generated planet name is assigned (mercury, venus, earth, etc.)
- If the positional argument contains `/`, it is treated as a branch name and the instance name is derived from it (e.g., `feature/foo` becomes `feature-foo`)
- Otherwise the positional argument is used as the instance name directly

**Git operations:**
After provisioning, `cspace up` runs `git fetch --prune` in the container. If a branch was specified, it checks out that branch and runs `git reset --hard` to the remote. Otherwise it runs `git pull --ff-only`.

**Launch modes:**
- **Interactive** (default): Opens a live Claude Code TTY session. Not routed through the supervisor.
- **Autonomous** (`--prompt` or `--prompt-file`): Routes through the agent supervisor with messenger MCP tools, a Unix control socket, inbox watcher, and structured event logging.
- **Headless** (`--no-claude`): Provisions the instance but does not launch Claude. Useful for pre-provisioning or manual work via `cspace ssh`.

### Examples

```bash
# Launch with auto-generated name
cspace up

# Launch a named instance
cspace up mercury

# Launch from a branch
cspace up feature/auth

# Name derived from feature/auth, but check out develop instead
cspace up feature/auth --base develop

# Provision without Claude
cspace up --no-claude mercury

# Run a one-shot autonomous agent
cspace up mercury --prompt "Fix the failing unit tests in src/auth"

# Run an autonomous agent with prompt from file
cspace up mercury --prompt-file ./tasks/fix-auth.md
```

---

## `cspace ssh`

Open a shell into a running instance.

### Syntax

```bash
cspace ssh <name>
```

### Description

Drops the user into an interactive bash shell as the `dev` user in the `/workspace` directory of the named instance. The instance must be running.

### Examples

```bash
cspace ssh mercury
```

---

## `cspace list`

List running instances.

### Syntax

```bash
cspace list [--all]
```

### Flags

| Flag | Description |
|------|-------------|
| `--all` | Show instances across all projects (adds a PROJECT column) |

### Description

Displays a table of running instances with their name, current git branch, and uptime. Without `--all`, only instances for the current project are shown.

`cspace ls` is accepted as an alias for `cspace list`.

### Output

```
# Project-scoped (default):
INSTANCE            BRANCH                         AGE
--------            ------                         ---
mercury             main                           2 hours ago

# With --all:
INSTANCE         PROJECT              BRANCH                         AGE
--------         -------              ------                         ---
mercury          my-project           main                           2 hours ago
venus            other-project        feature/auth                   15 minutes ago
```

### Examples

```bash
# List instances for the current project
cspace list

# List instances across all projects
cspace list --all
```

---

## `cspace ports`

Show port mappings for an instance.

### Syntax

```bash
cspace ports <name>
```

### Description

Displays the configured port mappings from `.cspace.json` with their labels, as well as any additional service ports from docker-compose. The instance must be running.

### Output

```
Ports for mercury:
  dev-server: http://localhost:3001
  preview: http://localhost:4174
```

### Examples

```bash
cspace ports mercury
```

---

## `cspace down`

Destroy an instance and its volumes.

### Syntax

```bash
cspace down <name>
cspace down --all
cspace down --everywhere
```

### Flags

| Flag | Description |
|------|-------------|
| `--all` | Destroy all instances for the current project |
| `--everywhere` | Destroy all cspace instances across all projects (requires confirmation) |

### Description

Removes containers and volumes for the specified instance using `docker compose down --volumes`.

- `cspace down <name>` — Removes a single instance
- `cspace down --all` — Removes all instances for the current project
- `cspace down --everywhere` — Removes all cspace instances globally. Displays a list of instances that will be destroyed and prompts for confirmation (interactive with `gum`, or text input fallback).

### Examples

```bash
# Destroy a single instance
cspace down mercury

# Destroy all project instances
cspace down --all

# Destroy everything (with confirmation prompt)
cspace down --everywhere
```

---

## `cspace warm`

Pre-provision containers without launching Claude.

### Syntax

```bash
cspace warm <name> [name...]
```

### Description

Provisions one or more instances in sequence, validates firewall initialization, and provides a summary table. Useful for pre-warming multiple containers before launching agents.

If the firewall has not been initialized in a container, `cspace warm` will re-initialize it automatically. Exits with code 1 if any container fails validation.

### Output

```
Warming 3 containers...

--- Setting up mercury ---
[setup output]

--- Setting up venus ---
[setup output]

--- Setting up earth ---
[setup output]

=========================================
INSTANCE             STATUS
--------             ------
mercury              ready
venus                ready
earth                ready
All 3 containers ready.
```

### Examples

```bash
# Warm a single container
cspace warm mercury

# Warm multiple containers
cspace warm mercury venus earth
```

---

## `cspace rebuild`

Rebuild the container image.

### Syntax

```bash
cspace rebuild
```

### Description

Builds the container image from scratch using `docker build --no-cache`. The build context is the cspace installation directory, which allows the Dockerfile to copy `bin/cspace` and `lib/` into the image. The Dockerfile is resolved via the template override system — a project-level `.cspace/Dockerfile` takes precedence over the default.

The image is tagged as `cspace-{project.name}` based on the project name in `.cspace.json`.

### Examples

```bash
cspace rebuild
```

---

## `cspace sync-context`

Generate a milestone context document.

### Syntax

```bash
cspace sync-context
```

### Description

Runs the `sync-context.sh` script to generate documentation of the current milestone state. The script is resolved via the override system — a project-level script in `.cspace/scripts/sync-context.sh` takes precedence over the default.

### Examples

```bash
cspace sync-context
```
