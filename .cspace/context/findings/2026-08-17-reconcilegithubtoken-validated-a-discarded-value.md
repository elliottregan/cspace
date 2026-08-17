---
title: ReconcileGitHubToken validated a value the later env_file merge discarded
date: 2026-08-17
kind: finding
status: resolved
category: bug
tags: credentials, github, cmd-up, validation
---

## Summary
`ReconcileGitHubToken` (`cmd_up.go:173`) performed the right liveness check —
a direct `GET https://api.github.com/user` — against the wrong subject.

`effectiveGH` was built from `loaded` plus host shell only
(`cmd_up.go:163-172`). The compose `env_file` value did not exist yet at line
173; it arrived at line 349 and overwrote the checked value. So the preflight
validated the *valid* host token, found nothing wrong, left `ghTokenOverride`
empty, and the escape hatch at line 528 never fired.

Measured on `cspace-resume-redux-mercury`: all three GitHub vars carried
sha1/8 `310607f9`, byte-identical to `GH_TOKEN` in the project's `.env`. The
host's `gh auth token` (`ab53e677`) appeared nowhere in the container. The
token that shipped was never validated by anything, while `doctor` reported a
green check.

Root cause is structural: `ReconcileGitHubToken` bundled verification with
fallback discovery, so both had to share one call site — and that call site
had to run before the merge that replaced its subject.

## Updates
### 2026-08-17 — @agent — status: resolved
Split into `credentials.Verify` (a pure predicate) and Bake's fallback ladder.
Verification now runs on the value that actually ships, after every merge.
Verified live: a fresh sandbox on the same project booted with `ab53e677`
under all three GitHub names, reported `github ← auto-discovered (verified)`.
