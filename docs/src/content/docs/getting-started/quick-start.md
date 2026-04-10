---
title: Quick Start
description: Get up and running with cspace in minutes.
sidebar:
  order: 2
---

This guide walks you through initializing cspace in a project and launching your first devcontainer instance.

## 1. Initialize your project

Navigate to your project root and run:

```bash
cd my-project
cspace init
```

This creates two things:

- **`.cspace.json`** — project configuration (commit this to git)
- **`.cspace/`** — directory for template overrides and custom scripts

cspace auto-detects your project name (from the directory name), GitHub repo (from `git remote`), and a two-character prefix for naming containers.

:::tip
Run `cspace init --full` to also copy the built-in templates (Dockerfile, docker-compose files, agent prompts) into `.cspace/` for customization.
:::

## 2. Set up Git authentication

Before launching an instance, add a GitHub personal access token to your project:

```bash
echo 'GH_TOKEN=ghp_yourTokenHere' >> .env
```

This is required for git push/pull inside containers. See [Git Authentication](/getting-started/git-authentication/) for details on creating a token with the right scopes.

## 3. Launch an instance

```bash
cspace up
```

This will:

1. Build the devcontainer image (first run only)
2. Start the container with your project code
3. Copy your git identity and `.env` into the container
4. Launch Claude Code inside the instance

Instances are automatically assigned planet names (mercury, venus, earth...) with deterministic port mappings. You can also specify a name or branch:

```bash
cspace up venus              # use a specific planet name
cspace up feature/my-branch  # check out a branch
cspace up --no-claude earth  # create instance without launching Claude
```

## 4. Develop

Once an instance is running, you have several ways to interact with it:

```bash
cspace ssh mercury      # shell into the instance
cspace list             # see all running instances
cspace ports mercury    # show port mappings
```

To run an autonomous agent against a GitHub issue:

```bash
cspace issue 42         # agent resolves issue #42 end-to-end
```

## 5. Clean up

When you're done with an instance:

```bash
cspace down mercury     # destroy a single instance
cspace down --all       # destroy all instances for this project
```

## Next steps

- [Git Authentication](/getting-started/git-authentication/) — set up your GitHub token and understand SSO requirements
- [Configuration Reference](/configuration/configuration-reference/) — customize ports, firewall, Claude model, and more
