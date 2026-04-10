---
title: Project Setup
description: Commands for initializing and updating cspace.
sidebar:
  order: 5
---

Commands for initializing cspace in a project directory and keeping cspace up to date.

## `cspace init`

Initialize cspace project configuration.

### Syntax

```bash
cspace init [--full]
```

### Flags

| Flag | Description |
|------|-------------|
| `--full` | Also copy all templates (Dockerfile, docker-compose, agent prompts) to `.cspace/` for customization |

### Description

Scaffolds the cspace configuration for the current project. Must be run from within a git repository.

**Auto-detection:**
- **Project name** — derived from the directory name
- **GitHub repo** — detected from the git remote URL
- **Label prefix** — first two characters of the project name

**Interactive setup:**
If `gum` is installed, `cspace init` presents interactive prompts for configuring:
- Project name
- GitHub repo (`owner/repo`)
- Label prefix (2–3 characters)
- Extra firewall domains (comma-separated)
- Verification command (e.g., `npm run lint && npm run test`)
- E2E test command (e.g., `npm run e2e`)

Without `gum`, auto-detected defaults are used.

### Created Files

| File | Description |
|------|-------------|
| `.cspace.json` | Project configuration file |
| `.cspace/` | Directory for project-specific overrides |

With `--full`, additional files are copied for customization:

| File | Description |
|------|-------------|
| `.cspace/Dockerfile` | Container image definition |
| `.cspace/docker-compose.core.yml` | Core compose configuration |
| `.cspace/agents/implementer.md` | Autonomous agent prompt |
| `.cspace/agents/coordinator.md` | Coordinator agent prompt |

### Default Configuration

The generated `.cspace.json` has the following structure:

```json
{
  "project": {
    "name": "my-project",
    "repo": "owner/my-project",
    "prefix": "my"
  },
  "container": {
    "ports": {},
    "environment": {}
  },
  "firewall": {
    "enabled": true,
    "domains": []
  },
  "claude": {
    "model": "claude-opus-4-6[1m]",
    "effort": "max"
  },
  "verify": {
    "all": "",
    "e2e": ""
  },
  "agent": {
    "issue_label": "ready"
  },
  "services": "",
  "post_setup": ""
}
```

If the project is already initialized (`.cspace.json` exists), `cspace init` exits without making changes and suggests editing the file directly.

### Examples

```bash
# Basic initialization
cd my-project
cspace init

# Full initialization with all templates
cspace init --full
```

---

## `cspace self-update`

Update cspace to the latest version.

### Syntax

```bash
cspace self-update
```

### Description

Updates cspace to the latest version by running `git pull --ff-only` in the cspace installation directory. Only works when cspace was installed via `git clone`. Displays the updated version tag or short commit hash after updating.

If cspace was not installed via git clone, the command exits with an error.

### Examples

```bash
cspace self-update
```
