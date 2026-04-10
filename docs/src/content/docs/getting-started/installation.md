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
| **jq** | Yes | JSON processing | `brew install jq` / `apt install jq` |
| **gum** | No | Interactive TUI menus | `brew install gum` |
| **gh** | No | GitHub CLI integration | `brew install gh` |

You will also need a **`GH_TOKEN`** in your project `.env` file for git push/pull from inside containers. See [Git Authentication](/getting-started/git-authentication/) for setup instructions.

## Install

Run the install script:

```bash title="Terminal"
curl -fsSL https://raw.githubusercontent.com/elliottregan/cspace/main/install.sh | bash
```

This will:

1. Clone the cspace repository to `~/.cspace` (or `$CSPACE_HOME` if set)
2. Make the `cspace` CLI executable
3. Add `~/.cspace/bin` to your `PATH` in the appropriate shell RC file
4. Install zsh completions (if you use zsh)

After installation, restart your shell or source your RC file:

```bash
source ~/.zshrc  # or ~/.bashrc, ~/.profile
```

Verify the installation:

```bash
cspace version
```

## PATH setup

The installer automatically detects your shell and appends to the correct RC file:

| Shell | RC file |
|-------|---------|
| zsh | `~/.zshrc` |
| bash | `~/.bashrc` |
| other | `~/.profile` |

The line added is:

```bash title="~/.zshrc (or ~/.bashrc, ~/.profile)"
export PATH="$HOME/.cspace/bin:$PATH"
```

If `.cspace/bin` is already in your RC file, the installer skips this step.

### Custom install location

To install cspace somewhere other than `~/.cspace`, set `CSPACE_HOME` before running the script:

```bash
CSPACE_HOME=/opt/cspace curl -fsSL https://raw.githubusercontent.com/elliottregan/cspace/main/install.sh | bash
```

## Shell completions

On **zsh**, the installer automatically sets up tab completions:

- Creates `~/.zsh/completions/` (or `$ZDOTDIR/.zsh/completions/`)
- Symlinks the completion file as `_cspace`

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

Or re-run the install script — it detects the existing installation and updates it via `git pull --ff-only`.

## Next steps

Continue to the [Quick Start](/getting-started/quick-start/) to initialize your first project and launch an instance.
