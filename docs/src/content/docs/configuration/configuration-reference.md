---
title: Configuration Reference
description: Complete reference for all cspace configuration options, config merging, and auto-detection.
sidebar:
  order: 1
---

import { Aside } from '@astrojs/starlight/components';

Every cspace project is configured through a `.cspace.json` file in the repository root. This page documents every configuration key, how configs are merged, and what gets auto-detected.

## Full example

```json title=".cspace.json"
{
  "project": {
    "name": "my-project",
    "repo": "owner/my-project",
    "prefix": "mp"
  },
  "container": {
    "ports": {
      "3000": "dev server",
      "4173": "preview server"
    },
    "environment": {
      "VITE_HOST": "0.0.0.0"
    }
  },
  "firewall": {
    "enabled": true,
    "domains": [
      "api.example.com",
      "cdn.example.com"
    ]
  },
  "claude": {
    "model": "claude-opus-4-7[1m]",
    "effort": "xhigh"
  },
  "mcpServers": {},
  "plugins": {
    "enabled": true,
    "install": [
      "superpowers",
      "context7",
      "code-review"
    ]
  },
  "verify": {
    "all": "npm run lint && npm run typecheck && npm run test",
    "e2e": "npm run e2e"
  },
  "agent": {
    "issue_label": "ready"
  },
  "services": ".cspace/docker-compose.yml",
  "post_setup": ".cspace/post-setup.sh"
}
```

## Schema reference

### `project`

Project identity. All three fields are auto-detected if left empty.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `project.name` | `string` | `""` | Project display name. Auto-detected from the directory name if empty. |
| `project.repo` | `string` | `""` | GitHub repository in `owner/repo` format. Auto-detected from `git remote` if empty. |
| `project.prefix` | `string` | `""` | Short prefix used for naming Docker resources (compose projects, containers). Auto-derived from the first 2 characters of `project.name` if empty. |

### `container`

Controls the devcontainer environment.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `container.ports` | `object` | `{}` | Port mappings exposed from the container. Keys are port numbers (as strings), values are human-readable descriptions. Example: `{"3000": "dev server"}`. |
| `container.environment` | `object` | `{}` | Environment variables injected into the container. Key-value pairs. Example: `{"VITE_HOST": "0.0.0.0"}`. |

### `firewall`

Egress firewall configuration. GitHub, npm, and Anthropic domains are always allowed regardless of this setting.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `firewall.enabled` | `boolean` | `true` | Enable the iptables-based egress firewall. When enabled, only whitelisted domains can be reached from inside the container. |
| `firewall.domains` | `array` | `[]` | Additional domains to whitelist for outbound traffic. |

### `claude`

Claude Code agent configuration. Both keys are plumbed into the container as the first-class Claude Code env vars ([`ANTHROPIC_MODEL`](https://code.claude.com/docs/en/env-vars) and `CLAUDE_CODE_EFFORT_LEVEL`) — they override `settings.json` and the `/effort` command.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `claude.model` | `string` | `"opus[1m]"` | Claude model to use. The `opus` alias always resolves to the latest Opus model; `[1m]` enables the 1M-token context window. Pin a specific version with e.g. `"claude-opus-4-7[1m]"` or switch classes with `"sonnet"`. Set to `""` to fall back to the Claude CLI account default. |
| `claude.effort` | `string` | `""` | Reasoning effort level. Accepted values: `low`, `medium`, `high`, `xhigh`, `max`, `auto`. When empty, the container env var defaults to `xhigh` for interactive use; autonomous supervisor runs bump to `max`. Any explicit value here applies everywhere. |

### `mcpServers`

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `mcpServers` | `object` | `{}` | MCP (Model Context Protocol) server configurations. Each key is a server name; the value is the server's config object. Passed through to Claude Code inside the container. |

### `plugins`

Controls automatic plugin installation from the official marketplace during instance setup.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `plugins.enabled` | `boolean` | `true` | Enable automatic plugin installation. Set to `false` to skip. |
| `plugins.install` | `array` | See below | List of plugin names to install from the official marketplace. |

The default `plugins.install` list includes: `superpowers`, `frontend-design`, `context7`, `code-review`, `code-simplifier`, `github`, `feature-dev`, `security-guidance`, `commit-commands`, `pr-review-toolkit`, `agent-sdk-dev`, `plugin-dev`.

Override the list in `.cspace.json` to install a different set, or disable entirely:

```json title=".cspace.local.json"
{
  "plugins": { "enabled": false }
}
```

### `verify`

Commands used to verify code before shipping.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `verify.all` | `string` | `""` | Shell command for running all verification (lint, typecheck, tests). Executed by agents after making changes. |
| `verify.e2e` | `string` | `""` | Shell command for running end-to-end tests. Executed separately from `verify.all` because E2E tests often require a running dev server. |

### `agent`

Controls autonomous agent behavior.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `agent.issue_label` | `string` | `"ready"` | GitHub label that marks issues as ready for autonomous processing. Used by the `/run-ready` workflow to find issues to work on. |

### Top-level keys

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `services` | `string` | `""` | Path to a Docker Compose file with project-specific services (e.g., `".cspace/docker-compose.yml"`). See [Project Services](/configuration/project-services/). |
| `post_setup` | `string` | `""` | Path to a shell script that runs after container initialization (e.g., `".cspace/post-setup.sh"`). See [Project Services](/configuration/project-services/). |

## Config merging

Configuration is loaded and merged in order — later sources override earlier ones using recursive deep merge (jq's `*` operator). You only need to specify the keys you want to override; everything else inherits from the previous layer.

| Priority | Source | Description |
|----------|--------|-------------|
| 1 (lowest) | `$CSPACE_HOME/lib/defaults.json` | Built-in defaults shipped with cspace. |
| 2 | `.cspace.json` | Project config, committed to git. Shared by the whole team. |
| 3 | `.cspace.local.json` | Local overrides, gitignored. Per-developer customization. |
| 4 (highest) | Environment variables | `CSPACE_PROJECT_NAME` and `CSPACE_PROJECT_REPO` override their respective config keys. |

<Aside type="tip">
`.cspace.local.json` is automatically added to `.gitignore` by `cspace init`. Use it for personal settings like extra firewall domains or different port mappings that shouldn't affect other developers.
</Aside>

### Merging example

Given these two files:

```json title="defaults.json"
{
  "firewall": { "enabled": true, "domains": [] },
  "claude": { "model": "opus[1m]", "effort": "" }
}
```

```json title=".cspace.json"
{
  "firewall": { "domains": ["api.example.com"] }
}
```

The merged result is:

```json title="Effective config"
{
  "firewall": { "enabled": true, "domains": ["api.example.com"] },
  "claude": { "model": "opus[1m]", "effort": "" }
}
```

The `firewall.enabled` default is preserved because `.cspace.json` only overrides `firewall.domains`.

## Auto-detection

When a field is left empty (or omitted), cspace auto-detects it at load time:

| Field | Detection method |
|-------|-----------------|
| `project.name` | Derived from the current directory name (`basename` of the git root). |
| `project.repo` | Extracted from the `origin` git remote URL. Supports both HTTPS (`https://github.com/owner/repo.git`) and SSH (`git@github.com:owner/repo.git`) formats. |
| `project.prefix` | First 2 characters of `project.name`. |

<Aside>
Auto-detection runs after config merging, so values set in `.cspace.json` or `.cspace.local.json` always take precedence over auto-detected values.
</Aside>

## Environment variable overrides

Two environment variables can override config values at runtime, taking the highest priority:

| Variable | Overrides |
|----------|-----------|
| `CSPACE_PROJECT_NAME` | `project.name` |
| `CSPACE_PROJECT_REPO` | `project.repo` |

These are applied after file-based merging but before auto-detection, so they also prevent auto-detection from running for those fields.

## Derived names

cspace computes several internal names from the configuration. These are not directly configurable but are useful to know when debugging container and volume naming:

| Name | Formula | Example |
|------|---------|---------|
| Compose project | `{prefix}-{instance}` | `mp-mercury` |
| Docker image | `cspace-{name}` | `cspace-my-project` |
| Memory volume | `cspace-{name}-memory` | `cspace-my-project-memory` |
| Logs volume | `cspace-{name}-logs` | `cspace-my-project-logs` |
| Instance label | `cspace.project={name}` | `cspace.project=my-project` |
