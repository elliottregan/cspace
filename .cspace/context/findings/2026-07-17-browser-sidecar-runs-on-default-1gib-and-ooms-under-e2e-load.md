---
title: browser sidecar runs on Apple Container's default 1 GiB and OOMs under e2e load
date: 2026-07-17
kind: finding
status: resolved
category: bug
tags: browser-sidecar, resources, oom, playwright, incident
---

## Summary
The shared browser sidecar's RunSpec sets no resource caps — neither `internal/cli/browser.go` nor the orchestrator mentions resources — so it runs on Apple Container's bare default (4 CPU / **1024 MiB**, no swap) while devcontainers get 16 GiB. The Playwright run-server spawns a fresh Chromium per e2e connection and the sidecar is shared by every sandbox in the project (three during the incident), so parallel e2e load walks it straight into guest memory exhaustion.

Incident 2026-07-17 (`cspace-resume-redux-browser`): guest kernel log showed `Free swap = 0kB` + SLUB `GFP_ATOMIC` allocation failures at ~281k pages (= the 1 GiB cap) after ~20h uptime. Failure shape is nasty: the guest kernel still ACKs TCP on :3000/:9222, but userspace (run-server, CDP, even vminitd's exec channel) is dead — so TCP-level health probes pass green while every consumer hangs silently in `browserType.connect`. A previous session hit the same wall as `ERR_INSUFFICIENT_RESOURCES` (see the 2026-05-06 finding); this is the same root cause, worse presentation.

## Details
- Fix: set explicit resources on the browser sidecar RunSpec (proposed: 4 GiB / 4 CPU to start; it hosts one long-lived CDP Chromium plus N per-connection run-server browsers). Applies on next sidecar recreation.
- Consider whether orchestrator-run compose services should also get the adapter's defaulted resources instead of the raw Apple Container default (convex-backend et al. currently run at 1024 MiB too — fine for them so far, but the default is applied by omission, not decision).
- Related findings from the same incident: `2026-07-17-sidecar-addressed-by-boot-baked-ip-no-recovery-path` (why the restart didn't restore service for existing sandboxes) and `2026-07-17-tcp-connect-probes-pass-wedged-services`.

## Updates
### 2026-07-18T01:55:00Z — @agent — status: open
filed during the 2026-07-17 sidecar OOM incident (resume-redux)

### 2026-07-18T02:20:00Z — @agent — status: resolved
`browserSidecarRunArgs` (extracted pure from `runBrowserSidecar`) now passes
`--cpus 4 --memory 4096MiB` explicitly; guarded by
`TestBrowserSidecarRunArgsSetsResourceCaps`. Takes effect on next sidecar
recreation by a binary containing the fix. The orchestrator-services question
(convex sidecars also on the raw 1024 MiB default) is noted in Details and
left open deliberately — those haven't misbehaved.
