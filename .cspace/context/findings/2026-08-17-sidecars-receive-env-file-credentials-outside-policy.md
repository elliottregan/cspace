---
title: Sidecar credential exposure is project-declared, not cspace-injected (corrected)
date: 2026-08-17
kind: finding
status: resolved
category: observation
tags: credentials, sidecars, compose, security, browser
---

## Summary
An earlier revision of this finding claimed that a credential in a project's
`.env` "still ships into every compose sidecar's env," framing it as a cspace
leak left open by the credential-resolution rewrite. **That claim was wrong.**
It came from reading `internal/sidecars/lifecycle.go:31` without measuring the
running containers.

Measured on a live Apple Container 1.2 host, `resume-redux` (the project whose
`.env` carried a dead fine-grained PAT and two other credential keys):

| Container | cspace credential keys | total env entries |
|---|---|---|
| `cspace-resume-redux-browser` | none | 4 |
| `cspace-resume-redux-mercury-convex-backend` | none | 3 |
| `cspace-resume-redux-mercury-convex-dashboard` | none | 4 |

The browser sidecar's entire environment is `PLAYWRIGHT_BROWSERS_PATH`, `PATH`,
`LANG`, `LC_ALL`. `internal/cli/browser.go` injects no environment into it at
all.

## Details

**cspace does not give sidecars credentials.** Two independent reasons:

1. `lifecycle.go:31` sets `Environment: svc.Environment` — that *service's own*
   compose environment, resolved per-service. It does not broadcast the
   workspace service's environment to sidecars, so `env_file` declared on the
   app service reaches the app service only.
2. `resume-redux/.devcontainer/docker-compose.yml` declares `env_file: ../.env`
   on `app` and on no other service. `convex-backend` and `convex-dashboard`
   never see it.

The browser sidecar is cspace-created and cspace-configured, and it needs no
tokens to run headless Chromium — its measured environment confirms it has
none.

**What remains, accurately scoped:** a project *may* declare `env_file:` or
`environment:` carrying credentials on one of its own sidecar services, and
that sidecar will then receive them. This is standard compose behavior,
byte-identical to what `docker compose up` would do with the same file, and it
is the project author's explicit choice about their own configuration. `Bake`'s
guarantee — that cspace-owned keys cannot be shadowed — is scoped to the
workspace sandbox, so cspace does not strip such values from a sidecar either.

That is a narrow, opt-in exposure rather than a cspace defect, and it
compounds the pre-existing `vminitd` env-logging issue only to the extent a
project opts into it.

## Updates
### 2026-08-17 — @agent — status: open
Logged during the credential-resolution rewrite, based on a code reading of
`lifecycle.go:31`, asserting that project `.env` credentials reach every
compose sidecar.

### 2026-08-17 — @agent — status: resolved
Corrected after measuring the running containers. The claim was wrong: no
sidecar in the motivating project carries any cspace credential key, the
browser sidecar carries four benign env vars, and sidecar environment is
resolved per-service rather than inherited from the workspace. Rewritten above
as an observation about project-declared compose configuration. No cspace
change is warranted.
