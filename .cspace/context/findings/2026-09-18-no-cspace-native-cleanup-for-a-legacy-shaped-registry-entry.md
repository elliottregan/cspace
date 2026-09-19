---
title: no cspace-native cleanup for a registry entry whose name fails the shape check
date: 2026-09-18
kind: finding
status: open
category: observation
tags: control-plane, cli, down, registry
---

## Summary
`validateSandboxName` rejects names that predate it (a dot, a separator, a
leading dash), and `cspace down --all` skips those entries rather than
tearing them down. But the single-name form fails hard on the same name, so
there is no cspace command that can clean one up: the skip message tells the
operator to run `cspace registry prune` and then delete two directories by
hand, and prune cannot do the second half at all.

## Details
`internal/cli/cmd_down.go:119-132`: under `--all`, a name that fails
`validateSandboxName` (`internal/cli/cmd_up.go:1299`) is skipped with

```
[cspace] skipping "dotted.name": ...
[cspace]   run `cspace registry prune` once its container is gone to drop the entry,
[cspace]   and delete ~/.cspace/clones/<project>/<name>/ and ~/.cspace/sessions/<project>/<name>/ by hand
```

and the single-name form returns the error instead (deliberately: there the
name came from the caller). So a legacy entry can be *named* by no command
that will act on it, and the guidance is a three-step manual procedure.

Two things make the guidance weaker than it reads:

1. **`cspace registry prune` only ever removed the registry row.** It has
   no knowledge of `~/.cspace/clones/` or `~/.cspace/sessions/`, so the
   directories the skip message names are always the operator's own job —
   which is exactly the half that matters, since the row is 200 bytes and
   the clone is a git checkout.
2. **Prune now keeps `stopped` entries** (Important 3 of the
   `control-plane-4a-pane-engine` review). A legacy-shaped entry that was
   last torn down with `--keep-state` is therefore *doubly* immune: the
   shape check keeps `down` off it, and its state keeps `prune` off it. The
   only remaining removal path for that row is editing
   `~/.cspace/sandbox-registry.json` by hand.

Nothing here is reachable from a name cspace itself would write today —
`validateSandboxName` runs on the `up` side too, so new entries all pass it.
The exposure is entries written before the check existed, on a developer's
own machine.

Fix, if it is worth one: a `cspace registry forget <project>:<name>` (or a
`--purge-state` flag on prune) that removes the row *and* the two
directories, keyed by the literal registry key rather than by a validated
name, so a row cspace can no longer address by name is still addressable by
key. That is the one operation the current command set cannot express.

## Updates
### 2026-09-18 — status: open
Filed from the `control-plane-4a-pane-engine` final review, which adjudicated
the parked note as a finding and asked that it record prune's new
keep-stopped behaviour as part of the picture.
