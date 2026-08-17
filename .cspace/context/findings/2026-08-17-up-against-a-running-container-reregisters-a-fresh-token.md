---
title: cspace up against a running container re-registers a fresh control token
date: 2026-08-17
kind: finding
status: resolved
category: bug
tags: cmd-up, registry, supervisor, agent
---

## Summary
`container run -d --name` fails when the name already exists and the adapter
has no adopt path (`adapter.go:179-279`), so `cspace up <existing>` cannot
re-bake in place. But the boot has already re-`Register`ed the sandbox with a
freshly generated control token (`cmd_up.go:795-807`) before the run fails.

The still-running sandbox's supervisor is holding the *old* token, so
`cspace send` and `cspace agent` against it now fail auth using the new
registry value.

Pre-existing, but newly load-bearing: `cspace doctor` now recommends
`cspace down <name> && cspace up <name>` as the remedy for a diverged
container, making this a documented hot path. A user who types only the `up`
half lands in this state.

## Updates
### 2026-08-17 — @agent — status: open
Surfaced during the credential-resolution rewrite's adversarial spec review.
Out of scope for that change; logged so the doctor remedy's failure mode is
recorded rather than discovered.

### 2026-08-17 — @agent — status: resolved
`ensureSandboxAvailable` now runs immediately after the Apple Container health
check, before the daemon spawn, clone provisioning, credential resolution, and
the early registry write. A name a container already holds fails there with an
actionable message instead of deep in the pipeline after the registry has been
overwritten.

Verified against a live sandbox: `cspace up mercury` while mercury was running
exited 1 with

```
Error: sandbox mercury already exists for project resume-redux.
  attach to it:  cspace attach mercury
  or replace it: cspace down mercury && cspace up mercury
```

and mercury's control token survived — `cspace agent status mercury` reported
`state: working`, and `cspace send` authenticated and drew a real reply from
the agent afterwards.

This also closes the explicit-name half of
`2026-07-16-custom-sandbox-names-bypass-collision-check`: auto-naming already
skipped taken names via pickPlanetName, and explicit names are now checked
too — the path agents use by convention, since descriptive names like
issue-142 are the documented recommendation for agent-spawned sandboxes.
