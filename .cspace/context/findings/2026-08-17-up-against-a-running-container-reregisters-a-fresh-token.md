---
title: cspace up against a running container re-registers a fresh control token
date: 2026-08-17
kind: finding
status: open
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
