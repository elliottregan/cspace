---
title: propagateFamily overwrote a project's distinct GITHUB_PERSONAL_ACCESS_TOKEN
date: 2026-08-17
kind: finding
status: resolved
category: bug
tags: credentials, github, cmd-up
---

## Summary
`propagateFamily` (`cmd_up.go:1395-1409`) picked the first non-empty value by
**name order**, with `GH_TOKEN` first, then wrote it to all three GitHub names.

`resume-redux/.env` declared `GITHUB_PERSONAL_ACCESS_TOKEN` as a *different*
93-char token (sha1/8 `8dfa8d5f`) from its `GH_TOKEN` (`310607f9`). The
propagation at `cmd_up.go:520` overwrote it before the container was created,
so the project's explicitly-set second token never reached the sandbox.

The comment at `cmd_up.go:518-519` — "No conflict warning here, so the
dual-write pattern is safe" — is about *consumer* tools (the GitHub family has
no analogue of the Claude CLI's dual-carrier prompt). It is not a claim about
cspace's own collision warning, and it did not cause the clobber.

## Updates
### 2026-08-17 — @agent — status: resolved
Replaced by the Mirror group policy in `internal/credentials/groups.go`, which
selects the highest-ranked candidate by **source rank** and mirrors it to all
three names. A name-order implementation now fails
`TestApplyGroupPolicyMirrorPicksBySourceRankNotNameOrder`.
