---
title: per-file Dockerfile COPYs and gitignored embedded/ tree both drift silently
date: 2026-07-16
kind: finding
status: open
category: bug
tags: dockerfile, apple-container, embed, build, drift
---

## Summary
Two related silent-drift traps in the asset pipeline:

1. **Dockerfile per-file COPYs.** Apple Container's builder does not recurse directory COPYs (the bug worked around in rc.34), so `lib/templates/Dockerfile` COPYs runtime scripts (`:154-157`) and plugin files (`:183-188`) one file at a time, with a `RUN test -f` guard covering only three hardcoded plugin paths. Meanwhile `make sync-embedded` uses globs (`cp lib/runtime/scripts/*.sh`, `cp -R lib/plugins/.`). A **new** file under `lib/runtime/scripts/` or `lib/plugins/` reaches the build context but is never COPY'd into the image — no build error, just a missing script/plugin at runtime inside the sandbox.
2. **Gitignored embedded tree.** `internal/assets/embedded/*` is gitignored (only `.gitkeep` tracked) and populated by `make sync-embedded`. A bare `go build ./cmd/cspace` or `go install …@latest` on a clean checkout embeds an empty tree and produces a binary whose asset reads fail only at runtime. Releases/CI are safe (goreleaser + ci.yml run sync-embedded); the trap is local builds and `go install`.

## Details
- Suggested directions: (1) have `sync-embedded` emit a manifest of expected files and add a Dockerfile `RUN` that diffs the manifest against what actually landed in the image — turning a forgotten COPY into a build failure; or generate the COPY block from the manifest. (2) add a startup sanity check in the Go binary (assets FS non-empty) with a clear "build via make" error message.
- History: the per-file COPY pattern was introduced deliberately (Apple Container 0.12.3 whole-dir COPY drops subdirectory contents); this finding is about the maintenance trap the workaround created, not about reverting it.

## Updates
### 2026-07-17T03:42:21Z — @agent — status: open
filed from the 2026-07-16 hardening survey
