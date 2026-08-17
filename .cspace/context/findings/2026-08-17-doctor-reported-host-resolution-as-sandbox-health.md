---
title: doctor reported host resolution while claiming to describe sandbox health
date: 2026-08-17
kind: finding
status: resolved
category: bug
tags: doctor, credentials, diagnostics
---

## Summary
`cspace doctor` and `cspace keychain status` re-resolved credentials on the
host at invocation time and reported the source they *would* pick now. Neither
read a running container's baked env, and neither validated against the
provider.

Credentials are baked into the container's init-process environment at create
time and are immutable for its life — `grep` finds zero re-injection anywhere
in the codebase. So host resolution describes what a *new* sandbox would get,
never what a running one holds.

The result: `doctor` printed `✓ GH_TOKEN: auto-discovered (gh auth token)`,
correctly describing host state, while the container held a dead PAT from an
entirely different source and every `git push` 401'd.

A second symptom of the same split: `credentialSource` (`cmd_keychain.go:209`)
was a parallel reimplementation of resolution that applied neither
`normalizeAnthropicCarrier` nor carrier exclusivity, so `keychain status` could
print `ANTHROPIC_API_KEY: auto-discovered` alongside `CLAUDE_CODE_OAUTH_TOKEN`
— a state no code path produces, since `autoDiscover` only ever filled the
OAuth carrier.

## Updates
### 2026-08-17 — @agent — status: resolved
`credentialSource` deleted; `keychain status` and `doctor` now render the same
`credentials.Resolve` output that boot uses, so the impossible row is
unrepresentable. `doctor` gains a "Sandbox credentials" section that reads each
registry-tracked sandbox's baked env via `container inspect` and reports
divergence from current host resolution. Sidecars are excluded — they
legitimately carry different env.
