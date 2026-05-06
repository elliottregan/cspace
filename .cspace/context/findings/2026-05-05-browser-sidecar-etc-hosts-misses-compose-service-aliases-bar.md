---
title: browser sidecar /etc/hosts misses compose-service aliases — bare-name URLs fail from headless chrome
date: 2026-05-05
kind: finding
status: resolved
category: bug
tags: devcontainer-adoption, browser-sidecar, etc-hosts, compose-orchestration, v1.0
related: https://github.com/elliottregan/cspace/issues/69, 2026-05-05-host-to-container-iteration-goes-through-git-remote-because
---

## Summary
cspace's orchestrator injects /etc/hosts entries (sandbox + each compose service by bare name, plus `workspace → 127.0.0.1` in the workspace) into the workspace and every compose-spawned sidecar. The cspace BROWSER sidecar (the headless-Chromium + Playwright run-server container spawned via `customizations.cspace.browser: true`) is NOT part of the compose plan, so it never receives this injection. Result: any URL the project bakes into its static bundle that uses a bare compose-service hostname (e.g. `http://convex-backend:3210`) resolves fine when fetched from the workspace but fails with `net::ERR_NAME_NOT_RESOLVED` when fetched by the headless chromium running in the browser sidecar.

## Details


## Updates
### 2026-05-05T17:03:47Z — @agent — status: open
filed

### 2026-05-05T17:12:51Z — @agent — status: resolved
## Correction — the proposed cspace-side fix is wrong

After thinking through the architectural shape: the orchestrator should NOT inject bare-name compose hostnames into the browser sidecar's /etc/hosts. Doing so would paper over the actual misconfiguration.

### The right separation

Bare-name hostnames (`convex-backend`, `convex-dashboard`, `app`, `workspace`) are **cluster-internal**. They're a convenience for code running inside the cluster — workspace SSR fetches, sibling-to-sibling service calls, post-create hooks. Routing those through `/etc/hosts` keeps the cluster's URLs portable across projects (the same compose file, written for Docker compose's default DNS, works in cspace).

A browser-served bundle is conceptually *outside* the cluster, even though cspace spawns the chromium that runs it. URLs in that bundle should be URLs a real user or agent could type into a real browser:

- **Public deployment URL** (e.g. `https://resumenotebook.app`) for production
- **Page-relative paths** (`/__convex/*`) proxied by the dev/preview server, for local dev — what `__SELF__` enables
- **cspace 4-label DNS** (`http://convex-backend.<sandbox>.<project>.cspace2.local:3210`) when direct multi-service access is genuinely needed and same-origin proxy isn't an option. cspace's daemon already serves these, and the browser sidecar's dnsmasq already forwards `*.cspace2.local` to it — no additional plumbing.

Bare-name compose hostnames in a browser bundle are a project misconfig. The cspace v1 canary's initial `VITE_CONVEX_URL=http://convex-backend:3210` was the misconfig (carried over from a misreading of the v0 setup, which actually stripped these vars).

### Resolution

Project-side fix is the right answer: drop browser-facing bare-name URLs from cspace v1's canary template. The Nuxt + Convex `__SELF__` proxy pattern is the standard, no cspace plumbing needed.

cspace-side: update docs (devcontainer-subset.md or a new "browser-bound URLs" section) to document the three valid patterns (same-origin proxy, public URL, 4-label cspace DNS) and to call out that bare compose hostnames are NOT one of them. Closing this finding as resolved — no code change against cspace, just a docs clarification follow-up.
