---
title: runtime versions come from the image, not the project — features runner exists but is unwired
date: 2026-08-28
kind: finding
status: acknowledged
category: refactor
tags: image, features, versioning, devcontainer, playwright, design
---

## Summary
A project's declared runtime versions have no effect on its sandbox. resume-redux
pins Node 26 in `.nvmrc`; its sandbox ran Node 24 because that is what the image
shipped. The mismatch surfaced as a suspected cause of a test flake (#1058) and
cost an agent a detour.

The mechanism to fix this is already written and not called. `internal/features/`
resolves six devcontainer feature IDs to installer scripts baked at
`/opt/cspace/features/` in the image, passing args as `FEATURE_<UPPER>` env vars,
and hard-rejects unknown IDs (registry-driven features were deferred to v1.1).
Nothing in the boot flow invokes it, so `features: {node: {version: "26"}}` in a
devcontainer.json is silently ignored today.

## Details
Version handling across cspace is inconsistent, and only one part derives from
the project:

- **Playwright** — `detectPlaywrightVersion` reads `@playwright/test` from the
  project's package.json and pins the sidecar image to that exact tag, falling
  back to `defaultPlaywrightVersion`. Derivation works; the fallback constant
  rots (it sat at 1.59.0 against a current 1.62.1 until 2026-08-28).
- **Node** — comes from the image's base tag, with no derivation at all.
- **MCP clients** — pinned in the Dockerfile, rotting the same way
  (`@playwright/mcp` and `chrome-devtools-mcp` were several releases behind).

**Direction chosen 2026-08-28: declared in devcontainer features.** Rejected
alternatives: deriving from repo files (`.nvmrc`, `engines`, `.python-version`)
was judged too much implicit cspace opinion about ecosystems; leaving everything
to the image means a plain repo with an `.nvmrc` silently runs the wrong runtime,
which is the status quo that produced this finding.

**The open question is when feature installs run**, and it was not settled:

- Bake into a per-project image keyed by base + feature-set, so the first
  `cspace up` pays and later sandboxes start instantly. Precedent exists in
  `sidecars.BuildProjectImage`. Cost: cache invalidation, image proliferation.
- Run at every boot from the entrypoint. Simplest, but `node.sh` curls `n` and
  downloads a Node build, so it adds real time to every boot and fails offline.
- Boot-time install cached in a per-project volume. Apple Container volumes are
  exclusive to one container at a time, which collides with several sandboxes
  per project.

Elliott's constraint, verbatim: *"anything complicated i want to address
wholistically, not as a patch. what we are discussing changes how the core of
cspace works, and i want to be careful with that."* A version bump was taken as
the interim step; this design is deliberately deferred, not forgotten.

## Updates
### 2026-08-28 — status: acknowledged
Interim: bumped the constants (Node 24→26, Playwright default 1.59.0→1.62.1,
MCP clients to current) rather than building provisioning machinery. Node
unbundling corepack at 25 was found during that bump — the image now installs
corepack explicitly, without which `corepack pnpm install` in a project's
post-create breaks.
