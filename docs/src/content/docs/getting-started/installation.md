---
title: Installation
description: How to install cspace on your machine.
sidebar:
  order: 1
---

## Prerequisites

Before installing cspace, make sure you have the following:

| Dependency | Required | Purpose | Install |
|-----------|----------|---------|---------|
| **Docker** with Compose v2 | Yes | Runs devcontainer instances | [docs.docker.com](https://docs.docker.com/get-docker/) |

:::tip
On macOS, [OrbStack](https://orbstack.dev) is a lightweight alternative to Docker Desktop with faster startup, lower resource usage, and built-in Linux VM support.
:::

You will also need a **`GH_TOKEN`** in your project `.env` file for git push/pull from inside containers. See [Git Authentication](/getting-started/git-authentication/) for setup instructions.

## Install

### Homebrew (macOS)

```bash title="Terminal"
brew tap elliottregan/cspace
brew install cspace
```

### Install script (macOS and Linux)

```bash title="Terminal"
curl -fsSL https://raw.githubusercontent.com/elliottregan/cspace/main/install.sh | bash
```

This will:

1. Download the correct pre-built binary for your OS and architecture from GitHub Releases
2. Verify the SHA-256 checksum
3. Ad-hoc sign the binary on macOS (required for Apple Silicon)
4. Add `~/.cspace/bin` to your `PATH` in the appropriate shell RC file
5. Install zsh completions (if you use zsh)

After installation, restart your shell or source your RC file:

```bash
source ~/.zshrc  # or ~/.bashrc, ~/.profile
```

Verify the installation:

```bash
cspace version
```

### Custom install location

To install cspace somewhere other than `~/.cspace`, set `CSPACE_HOME` before running the script:

```bash
CSPACE_HOME=/opt/cspace curl -fsSL https://raw.githubusercontent.com/elliottregan/cspace/main/install.sh | bash
```

### Specific version

To install a specific version:

```bash
VERSION=v0.1.0 curl -fsSL https://raw.githubusercontent.com/elliottregan/cspace/main/install.sh | bash
```

## Shell completions

On **zsh**, the installer automatically sets up tab completions:

- Creates `~/.zsh/completions/` (or `$ZDOTDIR/.zsh/completions/`)
- Generates completions from the binary as `_cspace`

After installation, you get tab completion for all cspace commands, instance names, and planet names:

```bash
cspace <TAB>          # shows all commands
cspace up <TAB>       # suggests planet names (mercury, venus, earth...)
cspace ssh <TAB>      # suggests running instance names
cspace down <TAB>     # suggests running instance names
```

:::note
Shell completions are only installed for zsh. Bash and other shells are not currently supported for tab completion.
:::

## Updating

To update cspace to the latest version:

```bash
cspace self-update
```

Or re-run the install script — it detects the existing installation and replaces the binary.

## Next steps

Continue to the [Quick Start](/getting-started/quick-start/) to initialize your first project and launch an instance.
