---
title: Installation
description: How to install cspace on macOS and prepare the host for sandboxes.
sidebar:
  order: 1
---

cspace v1 is macOS-only (Apple Container substrate). Linux substrate support is post-1.0.

## Install the CLI

**Stable** (currently v0.x; v1 ships when v1.0.0 is tagged):

```bash
brew install elliottregan/cspace/cspace
```

**Release candidate** (v1.0.0-rc.x):

```bash
brew install elliottregan/cspace/cspace-rc
```

The two formulas are mutually exclusive — pick one. Verify:

```bash
cspace version
```

## Build the sandbox image

cspace v1 needs a local sandbox image (`cspace:latest`). Until image distribution lands ([#68](https://github.com/elliottregan/cspace/issues/68)), this is a one-time per-host step:

```bash
cspace image build
```

Takes 1–2 minutes on a warm machine. Idempotent — re-running is fast if the embedded library hasn't changed.

## Install the DNS resolver

cspace serves a small DNS zone (`*.cspace2.local`) so sandboxes are reachable by hostname from the host browser:

```bash
cspace dns install
```

This writes `/etc/resolver/cspace2.local` and prompts for sudo. One-time per host.

## Verify

```bash
cspace doctor
```

Should show all green for: Apple Container CLI, cspace daemon (HTTP + DNS), DNS routing, and credentials.

## Next

[Quick start →](../quick-start/)
