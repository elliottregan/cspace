---
title: Apple Container health check passes on any output containing "running"
date: 2026-07-16
kind: finding
status: resolved
category: bug
tags: applecontainer, substrate, health-check, cmd-up
---

## Summary
`Adapter.HealthCheck` (`internal/substrate/applecontainer/adapter.go:105-120`) lowercases `container system status` output and reports healthy if it contains the substring `"running"`. Output like "apiserver is not running" **contains** "running", so a stopped apiserver can be reported healthy (false positive); conversely, any future wording that drops the word fails closed. This check gates every `cspace up`, so a false positive surfaces later as confusing provisioning failures instead of the real "Apple Container isn't running" message.

## Details
- The comment in the code acknowledges "outputs vary" — the substring match was a pragmatic first pass.
- Suggested direction: check for negative markers first ("not running", "stopped"), prefer the command's exit code or a structured/parseable output mode if `container system status` offers one, and enumerate actual observed outputs across Apple Container versions in a test. The exec call is already isolated in the adapter, so this is a small, well-testable fix (seam: inject the command output).
- Priority: flagged by Elliott as a top hardening priority (2026-07-16).

## Updates
### 2026-07-17T03:42:21Z — @agent — status: open
filed from the 2026-07-16 hardening survey

### 2026-07-17T04:20:00Z — @agent — status: resolved
`HealthCheck` now delegates to `parseSystemStatus`, which prefers the 0.12.x
FIELD/VALUE table's `status` row (exact value match) and fails closed on
negated ("not running") or unrecognized output. Table-driven unit tests cover
the false-positive case plus real 0.12.3 table output; the live read-only
`TestHealthCheckRunning` integration test still passes against a running
apiserver.
