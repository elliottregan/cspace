---
title: CLI overview
description: Every cspace command, grouped by purpose.
sidebar:
  order: 1
---

Run `cspace --help` for the same listing in your terminal.

## Sandbox lifecycle

| Command | Purpose |
|---|---|
| `cspace up [<name>]` | Boot a sandbox. No name = auto-pick from planet list. |
| `cspace down <name>` | Stop and remove a sandbox. |
| `cspace down --all` | Tear down every sandbox in the current project. |
| `cspace attach <name>` | Open an interactive `claude` session inside a running sandbox. |
| `cspace send <name> "<text>"` | Inject a turn into the supervisor's session (non-interactive). |
| `cspace ports <name>` | List ports the sandbox is listening on, with friendly URLs. |

## Inspection

| Command | Purpose |
|---|---|
| `cspace doctor` | Aggregate health check across all subsystems. |
| `cspace registry list` | Show all registered sandboxes (any project). |
| `cspace registry prune` | Remove stale registry entries. |
| `cspace version` | Print the installed version. |

## Host setup

| Command | Purpose |
|---|---|
| `cspace image build` | Build the local sandbox image (one-time per host until image distribution lands). |
| `cspace dns install` | Write `/etc/resolver/cspace2.local` (sudo). |
| `cspace dns uninstall` | Remove the resolver routing. |
| `cspace dns status` | Verify DNS routing end-to-end. |
| `cspace daemon serve` | Run the registry HTTP + DNS daemon (auto-spawned by `cspace up`). |
| `cspace daemon status` | Is the daemon running? |
| `cspace daemon stop` | Stop the daemon (rarely needed). |

## Credentials

| Command | Purpose |
|---|---|
| `cspace keychain init` | Walk through storing API keys in macOS Keychain. |
| `cspace keychain status` | Show where each credential is currently sourced from. |

## Updates

| Command | Purpose |
|---|---|
| `cspace self-update` | Update the cspace binary in place. |
| `cspace completion <shell>` | Generate shell completions. |

## Per-command help

```bash
cspace <command> --help
```

For example:

```bash
cspace up --help        # all flags including --cpus, --memory, --browser, --no-attach
cspace registry --help  # list / prune
```
