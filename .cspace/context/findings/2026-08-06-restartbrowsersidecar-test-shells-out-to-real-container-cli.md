---
title: TestRestartBrowserSidecarMissingContainer is non-hermetic — depends on the host's live vmnet gateway
date: 2026-08-06
kind: finding
status: resolved
category: bug
tags: tests, browser-sidecar, gateway, applecontainer, hermeticity
---

## Summary
`TestRestartBrowserSidecarMissingContainer` (`internal/cli/browser_test.go:420`) fails on any machine where Apple Container isn't installed or running. It asserts the sidecar `run` argv built with gateway `192.168.65.1`, but the code path under test calls `resolveHostGateway` (`internal/cli/gateway.go:21`), which shells out to the real `container network inspect default`. When that call fails, `resolveHostGateway` falls back to `legacyVmnetGateway` (`192.168.64.1`) and the argv comparison fails on the embedded dnsmasq `server=/cspace.test/…#5354` line.

`make test` is therefore red out of the box on a fresh checkout — the whole rest of the suite passes and `go vet` is clean, so this one test is the only thing standing between a new clone and a green `make check`.

## Details
- Reproduced on a machine with Go 1.26.5 and no running Apple Container apiserver: `go test ./internal/cli/ -run TestRestartBrowserSidecarMissingContainer` fails with expected `192.168.65.1` / actual `192.168.64.1`.
- Confirmed pre-existing and unrelated to the change that surfaced it (docs-site removal): it fails identically on a pristine `HEAD` with all working-tree changes stashed.
- Introduced by the dynamic-gateway work (`c0ee224` "Derive the vmnet gateway dynamically for Container 1.1.x"). The derivation itself is correct and well-motivated — Apple moved the subnet `192.168.64` → `192.168.65` at the 1.0 boundary, so hardcoding was the bug being fixed. The gap is only that the *test* now reaches the host.
- The sibling test at `internal/cli/browser_test.go:20` passes the gateway explicitly into `browserSidecarRunArgs`, so it is hermetic. Only the restart path picks the gateway up implicitly.
- Suggested direction: introduce a seam so the restart path takes the gateway as a parameter (or an injectable func field, matching the injectable-probe pattern already used for `cspace browser status` at `browser_test.go:237`) and have the test pass a fixed value. That keeps the live derivation in production while making the test independent of host state.
- Note `internal/cli/probes_test.go:12` already documents the related "the vmnet gateway address doesn't exist on a dev machine" hazard for the DNS probe, so this class of host-dependence is a known pattern worth a consistent seam.

## Updates
### 2026-08-06 — @agent — status: open
Filed after hitting the failure while verifying an unrelated docs-site removal. Not fixed: out of scope for that change, and the fix is a deliberate API choice (parameter vs. injectable field) better made by someone with context on the substrate seam conventions.

### 2026-08-06 — @agent — status: resolved
Resolved with the injectable-field option, matching the `browserExecCmd` /
`verifyBrowserFn` convention already in `internal/cli/browser.go` rather than
threading a new parameter through the ladder. Added `resolveHostGatewayFn`
(`internal/cli/gateway.go`) defaulting to the real `resolveHostGateway`; both
sidecar-creation sites in `browser.go` now call through it, and
`browser_test.go` gained a `swapGateway` helper used by
`TestRestartBrowserSidecarMissingContainer` to pin `192.168.65.1`. `make check`
is green on a machine with no Apple Container apiserver running.

Two notes for anyone reading this later. The recreate branch is the *only*
ladder path that resolves the gateway, so the other `restartBrowserSidecar`
tests never needed pinning. And the suspicion that these calls explained the
~24s `internal/cli` runtime was wrong — measured before and after, it is
unchanged at ~24.5s, so the slow test is something else and still unidentified.
