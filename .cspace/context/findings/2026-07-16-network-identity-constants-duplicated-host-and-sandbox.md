---
title: network identity constants duplicated across host and sandbox with no single source of truth
date: 2026-07-16
kind: finding
status: open
category: refactor
tags: dns, daemon, constants, drift, entrypoint
---

## Summary
The gateway IP `192.168.64.1`, ports `6280`/`5354`, and the `cspace-<project>-<name>` container-name format are re-derived as string literals in 5+ files: `cmd_up.go:228-229` (env injection), `browser.go:130` (sidecar dnsmasq config), `probes.go`, `cmd_dns.go`, `cmd_registry.go:83`, `internal/orchestrator/lifecycle.go:140`, `cmd_attach.go:33`. None reference the authoritative constants in `cmd_daemon.go:27,40`. The daemon honors `CSPACE_DAEMON_DNS_ADDR`/`CSPACE_DAEMON_GATEWAY_ADDR` overrides, but the sandbox-side dnsmasq forwarder is written from a **single-quoted** heredoc (`cspace-entrypoint.sh:168`) and cannot be repointed — set an override (or let Apple Container assign a different vmnet subnet) and the daemon binds one place while sandboxes query another: `*.cspace.test` silently NXDOMAINs in-sandbox while external DNS keeps working.

## Details
- Same pattern in miniature: two independent `/etc/hosts` injectors with divergent escaping strategies — `internal/orchestrator/hosts.go:18-56` (real newlines + shellQuote) vs `internal/cli/browser.go:230-259` (literal `\n` two-char sequences relying on `printf` interpretation, its own duplicate BEGIN/END markers and sed). Any fix must currently be applied twice.
- Suggested direction: one package owning network identity (gateway addr, ports, DNS suffix, container-name format, FQDN builder), consumed by CLI, daemon, probes, orchestrator, and injected into the entrypoint/sidecar configs via env or template interpolation rather than baked literals. Collapse the two hosts injectors into the orchestrator one.

## Updates
### 2026-07-17T03:42:21Z — @agent — status: open
filed from the 2026-07-16 hardening survey
