---
title: DNS handler lowercases queries but name builders and registry entries are not case-normalized
date: 2026-07-17
kind: finding
status: open
category: bug
tags: dns, daemon, naming, case
---

## Summary
`daemonDNSHandler` lowercases incoming query names, but `browserSandboxHost`/`browserSingletonName` don't lowercase the project, and 2-label sandbox resolution compares the lowercased query against as-registered entry names. A mixed-case project or sandbox name therefore NXDOMAINs end-to-end (query arrives lowercased, container/entry names keep their case). `workspaceFriendlyHost` (browser.go) shows the intended convention — it lowercases both labels.

## Details
- Pre-existing hazard class, surfaced (not introduced) by the browser-DNS work in the 2026-07-17 final review.
- Fix direction: normalize once at the boundaries — lowercase project/sandbox when building container names, DNS hostnames, and registry entries (or reject mixed-case names in `validateSandboxName` and at project-name derivation), rather than sprinkling `strings.ToLower` at compare sites.
- Practically low-impact today: project names derive from repo directory names and sandbox names from planets/agent labels, which are conventionally lowercase — but nothing enforces it.

## Updates
### 2026-07-18T14:00:00Z — @agent — status: open
filed from the browser-sidecar branch's final whole-branch review
