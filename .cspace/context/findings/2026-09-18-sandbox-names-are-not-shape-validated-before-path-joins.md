---
title: sandbox names are not shape-validated before they're joined into teardown paths
date: 2026-09-18
kind: finding
status: resolved
category: bug
tags: control-plane, cli, down, security
---

## Summary
`wipeSandboxState` in `internal/cli/cmd_down.go` joins the caller-supplied
sandbox `name` straight into two host filesystem paths and then
`os.RemoveAll`s both, with no check on the name's shape (no rejection of
`..`, path separators, or other traversal-shaped input). This was a known,
deliberate gap left open by the control-plane-3 plan, which required a
finding to be filed for it before rollout step 4 — this is that finding.

## Details
`clonePath := filepath.Join(home, ".cspace", "clones", project, name)`
(`internal/cli/cmd_down.go:300`) is passed straight to `os.RemoveAll(clonePath)`
(`cmd_down.go:301`), and `sessionsPath := filepath.Join(home, ".cspace",
"sessions", project, name)` (`cmd_down.go:304`) to
`os.RemoveAll(sessionsPath)` (`cmd_down.go:306`) — both inside
`wipeSandboxState`, called from `teardownSandbox` whenever `wipeState` is
true (the `cspace down` default, i.e. no `--keep-state`). Neither `project`
nor `name` is validated before the join; `filepath.Join` does not reject
`..` segments.

The plan's Global Constraints record this as a deliberate, scoped deferral:

> **Sandbox-name shape validation in `up`/`down` is deferred**, deliberately.
> The exposure it would close is `cmd_down.go`'s
> `os.RemoveAll(clonePath)`/`os.RemoveAll(sessionsPath)`, which join a
> sandbox name into a host path. This plan adds no new input to it: every
> name the dashboard hands `Up`/`Down` comes off a `control.Row`, which
> `Correlate` builds from registry entries and `container ls` — never from a
> person typing into the UI, which has no free-text sandbox field. The check
> belongs in `cmd_up.go`/`cmd_down.go` with the rest of name handling, not in
> the dashboard. No finding file records it yet; file one under
> `.cspace/context/findings/` before rollout step 4, which is where a name
> could first arrive from somewhere else.
> (`docs/superpowers/plans/2026-09-18-control-plane-3-dashboard.md:43`)

As of this branch, the exposure remains theoretical: the dashboard has no
free-text sandbox field, and every name it passes to `Down` originates from
a `control.Row` that `Correlate` built from registry entries and
`container ls`. But nothing in `cmd_up.go`/`cmd_down.go` itself enforces a
name shape — the guard the plan describes does not exist yet — and rollout
step 4 is exactly the point where a new source of names (a typed sandbox
name, a scripted/remote caller, etc.) could reach `Down` for the first time.

Fix: add a shape check (reject empty names, path separators, and `.`/`..`
segments — likely alongside `ensureSandboxAvailable`/name handling in
`cmd_up.go`) applied uniformly to `cmd_up.go` and `cmd_down.go`, before step
4 introduces any caller that isn't already provably a `container ls`-derived
name.

## Updates
### 2026-09-18 — status: open
Filed from the control-plane-3-dashboard final review, carried forward from
step 2 of the plan's Global Constraints, which deferred this validation and
required a finding file to record it before rollout step 4.

### 2026-09-18 — status: resolved
`validateSandboxName` now enforces a single-label shape (letters, digits,
dashes, underscores; no dots, no separators, no leading dot or dash; at most
63 characters) and `cspace down` calls it for every name it is about to tear
down, `--all` included. The exposure closed is `wipeSandboxState`'s two
`os.RemoveAll`s.

To be accurate about the trigger: nothing in rollout step 4 actually types a
sandbox name into these paths. Its new-pane picker chooses among four fixed
pane kinds and its boot action passes a registry-derived row, so every name
still arrives from the registry or from `container ls`. The check lands
*ahead* of a UI that can type one rather than because of one — the step-3
deferral's premise was that no such UI would exist, and step 4 is where the
dashboard stopped being a pure reader of its own row set.
