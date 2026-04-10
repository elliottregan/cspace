---
title: Template Overrides
description: Customizing the Dockerfile, Docker Compose files, and agent prompts used by cspace.
sidebar:
  order: 3
---

import { Aside } from '@astrojs/starlight/components';

cspace ships with built-in templates for the container image, Docker Compose configuration, and agent prompts. You can override any of these by placing a file in the `.cspace/` directory of your project.

## Override points

There are five template override points. Place the override file at the specified path in your project root:

| Override path | What it replaces | Use case |
|---------------|-----------------|----------|
| `.cspace/Dockerfile` | Container image build | Add system dependencies, change the base image, or install custom tooling. |
| `.cspace/docker-compose.core.yml` | Core devcontainer service definition | Change resource limits, add capabilities, or modify the core container configuration. |
| `.cspace/docker-compose.shared.yml` | Browser sidecar services (Playwright, Chromium) | Customize browser versions, add extra sidecar containers, or change browser configuration. |
| `.cspace/agents/implementer.md` | Autonomous agent prompt | Customize how the agent explores code, designs solutions, and ships PRs for `cspace issue`. |
| `.cspace/agents/coordinator.md` | Multi-agent coordinator prompt | Customize how the coordinator manages parallel agents, resolves dependencies, and sequences work. |

<Aside type="note">
Template overrides are resolved at runtime. cspace checks for a project override first; if none exists, it falls back to the built-in default in `$CSPACE_HOME/lib/templates/`.
</Aside>

## How resolution works

cspace uses a simple precedence system for three categories of overridable files:

### Templates

Resolved by `resolve_template()` — used for Dockerfiles and Compose files.

```
.cspace/{name}  →  $CSPACE_HOME/lib/templates/{name}
 (project)           (built-in default)
```

### Scripts

Resolved by `resolve_script()` — used for setup and lifecycle scripts.

```
.cspace/scripts/{name}  →  $CSPACE_HOME/lib/scripts/{name}
     (project)                (built-in default)
```

### Agent prompts

Resolved by `resolve_agent()` — used for agent and coordinator prompts.

```
.cspace/agents/{name}  →  $CSPACE_HOME/lib/agents/{name}
    (project)                (built-in default)
```

In all cases, the project override takes precedence. If no override exists, the built-in default is used.

## Scaffolding overrides

Run `cspace init --full` to copy all built-in templates into your `.cspace/` directory. This gives you a starting point for customization:

```bash
cspace init --full
```

This copies the default Dockerfile, Compose files, and agent prompts into `.cspace/` so you can edit them in place.

<Aside type="tip">
You don't need to override everything. Only create override files for the templates you want to customize — cspace will use the built-in defaults for everything else.
</Aside>

## Customizing the Dockerfile

Override the container image to add system dependencies or custom tooling:

```dockerfile title=".cspace/Dockerfile"
# Start from the default cspace base or your own
FROM ubuntu:24.04

# Add project-specific system dependencies
RUN apt-get update && apt-get install -y \
    postgresql-client \
    imagemagick \
    && rm -rf /var/lib/apt/lists/*

# The rest of the cspace setup is handled by the entrypoint
```

## Customizing agent prompts

The implementer prompt controls how `cspace issue <num>` works — how the agent explores the codebase, designs solutions, and ships PRs. Override it to add project-specific instructions:

```markdown title=".cspace/agents/implementer.md"
<!-- Your custom implementer prompt -->
<!-- This replaces the built-in prompt entirely -->
```

The coordinator prompt controls how `cspace coordinate` manages multi-agent workflows — dependency resolution, parallel execution, and final review. Override it for custom coordination strategies:

```markdown title=".cspace/agents/coordinator.md"
<!-- Your custom coordinator prompt -->
<!-- This replaces the built-in prompt entirely -->
```

<Aside type="caution">
Agent prompt overrides replace the entire built-in prompt, not just parts of it. Start from the default (use `cspace init --full` to scaffold it) and modify from there to avoid losing important behavior.
</Aside>

## Customizing Compose files

### Core service

The core Compose file defines the devcontainer service itself — resource limits, capabilities, volume mounts, and network configuration. Override it when you need to change fundamental container behavior:

```yaml title=".cspace/docker-compose.core.yml"
# Your custom core service definition
# Replaces the built-in devcontainer Compose configuration
```

### Shared services

The shared Compose file defines browser sidecars (Playwright run-server and headless Chromium). Override it to change browser versions or add extra shared services:

```yaml title=".cspace/docker-compose.shared.yml"
# Your custom shared services definition
# Replaces the built-in browser sidecar configuration
```

<Aside type="note">
For adding project-specific services like databases alongside the devcontainer (without replacing the core or shared definitions), use the `services` config key instead. See [Project Services](/configuration/project-services/).
</Aside>
