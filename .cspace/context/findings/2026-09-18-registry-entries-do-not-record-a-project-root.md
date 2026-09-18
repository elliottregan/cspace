---
title: registry entries do not record the project root, so the control plane cannot `up` a sandbox of another project
date: 2026-09-18
kind: finding
status: open
category: observation
tags: control-plane, registry
---

## Summary
`control.Client.Up(ctx, sandbox)` boots a sandbox by shelling out to this
same `cspace` binary's `up` command, run in `Options.ProjectRoot` — one
directory, fixed when the Client is built. That is single-project by
construction: nothing in `registry.Entry` (`internal/registry/registry.go`)
records which project root a running (or previously-registered) sandbox
belongs to, so there is no data this package could read to resolve a second
project's root even if `Up` grew a `project` parameter. A control plane
watching several projects at once — the eventual point of this package —
cannot yet `up` a sandbox belonging to any project but the one the process
itself started in.

## Details
`cspace up` today learns its project root the same way every other cspace
command does: from the caller's cwd (`internal/cli/root.go`'s `projectName`/
config-loading path), never written anywhere durable. `registry.Entry` (the
struct `Correlate`, `AgentStatus`, `Send`, `Interrupt` and `Ports` all key
off) carries `ControlURL`, `Token`, `IP`, `StartedAt`, `BrowserContainer` and
`State` — no project root, no clone path, nothing that would let a
process with a different cwd (or no meaningful cwd at all, like a daemon or
a TUI) find the `.cspace.json`/`.devcontainer` a given project's `up` needs.

`Client.Up` is scoped to rollout step 2 of the control-plane design
(single-project), so this is a recorded limitation, not a regression — see
`internal/control/up.go`'s `Up` doc comment, which now points here. It
becomes a real gap the moment a control-plane process is asked to `up` a
sandbox for a project it wasn't started in.

Fix candidates, none chosen:

1. **Record the root at `cspace up` time (preferred).** Add a project-root
   field to `registry.Entry`, written by `cmd_up.go` the same place it writes
   `ControlURL`/`State`. Every later reader (`Client.Up` among them) then has
   what it needs without inventing a second source of truth for "where is
   this project checked out."
2. **`Up(ctx, project, sandbox)` that resolves the root itself.** Pushes the
   problem into `internal/control`, which would need some other way to map a
   project name to a filesystem root — plausibly a per-project config the
   daemon already tracks, or a convention over `~/.cspace/clones/<project>/`
   (see `CloneDir`, `paths.go`) if every sandbox of a project is guaranteed
   to share one root, which is not currently guaranteed.

(1) is preferred: it puts the fact where every other reader already looks
(the registry entry), rather than growing a second resolution path that has
to agree with it.

## Updates
### 2026-09-18 — status: open
Filed during the final-review fix wave for plan 2 (control API extraction),
which ruled `Up` stays single-project for this branch and required this
finding as the recorded follow-up (`internal/control/up.go`'s `Up` doc
comment references it).
