---
title: TestCspaceLifecycle mutates host state — replaces the real daemon, can trigger a cspace:latest rebuild
date: 2026-07-16
kind: finding
status: open
category: bug
tags: tests, integration, daemon, image, dev-safety
---

## Summary
`TestCspaceLifecycle` (`internal/cli/cmd_integration_test.go`) execs the real `bin/cspace-go up` against the host, gated only on substrate readiness (container CLI present, apiserver healthy, `cspace:latest` present). On a normal dev machine those gates all pass, so a plain `go test ./internal/cli` has real side effects:

1. **Replaces the running daemon.** The up-flow's version handshake sees the dev binary ≠ the installed daemon's version, `pkill`s the real daemon, and spawns `bin/cspace-go daemon serve` in its place. Observed 2026-07-16: the rc.36 daemon was replaced mid-session while production resume-redux sandboxes (venus/mercury) were live. No user-visible breakage — the dev daemon was code-identical and DNS kept resolving — but only by luck of the diff.
2. **Consents to rebuilding the shared image.** The image-staleness prompt defaults to Y on non-interactive stdin, so the test agrees to rebuild `cspace:latest`. With a dev version string the release-tarball fetch 404s and the test **fails** (the default state during development: any checkout whose `git describe` differs from the image's label). If a matching release existed, it would silently rebuild the user's image out from under running workflows.
3. **Live network calls**: the GitHub token preflight runs for real (api.github.com + `gh auth token` fallback).

Sandbox/clone cleanup is handled well (unique `int-*` name, `down` + clone removal in Cleanup); the observed failed run leaked nothing.

## Details
- Failure mode as experienced: `go test ./internal/...` on a healthy dev machine is red (`FAIL internal/cli`) for environment reasons unrelated to the code under test, and the "failure" leaves the host daemon swapped.
- Suggested hardening, in priority order:
  1. Gate the test behind an explicit opt-in (e.g. `CSPACE_E2E=1`), so default `go test ./...` is side-effect-free; skip with a message naming the env var.
  2. Make the invocation prompt-safe: run `up` with an explicit no-rebuild answer/flag rather than inheriting the non-interactive default-Y. (Separately worth revisiting as a production-UX question: should a destructive image rebuild ever default to Y when stdin isn't a TTY?)
  3. Consider isolating the daemon via the `CSPACE_DAEMON_DNS_ADDR`/`CSPACE_DAEMON_GATEWAY_ADDR` overrides so the test never touches the real one — with the trade-off that it then exercises less of the real DNS path.
- Related: the daemon-replacement blast radius is what the pkill finding (`2026-07-16-daemon-stop-system-wide-pkill-and-tcp-only-port-wait`) is about; fixing that shrinks this one too.

## Updates
### 2026-07-17T04:20:00Z — @agent — status: open
filed after `go test ./internal/...` (run during the health-check fix) replaced the live rc.36 daemon and attempted an image rebuild on a machine with production sandboxes running
