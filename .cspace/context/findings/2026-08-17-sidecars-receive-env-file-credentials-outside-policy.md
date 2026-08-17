---
title: Compose sidecars receive env_file credentials outside any cspace policy
date: 2026-08-17
kind: finding
status: open
category: bug
tags: credentials, sidecars, compose, security
---

## Summary
`internal/sidecars/lifecycle.go:31` passes a compose service's
`svc.Environment` verbatim, which includes anything compose-go resolved from
`env_file:` entries. A credential sitting in a project's `.env` therefore still
ships into every compose sidecar's environment.

`internal/credentials`' Bake governs the **workspace sandbox only**. The
guarantee "a project env_file cannot shadow a cspace credential" is scoped to
that container and does not extend to sidecars.

This also compounds the `vminitd` env-logging finding: each sidecar is another
process whose full environment is readable via `container logs`.

## Updates
### 2026-08-17 — @agent — status: open
Logged while landing the credential-resolution rewrite and deliberately left
out of its scope: closing it requires deciding what credentials a sidecar
legitimately needs, which is a separate design question from workspace
credential ownership.
