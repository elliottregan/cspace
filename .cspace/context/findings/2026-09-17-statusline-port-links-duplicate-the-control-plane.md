---
title: statusline port links duplicate what the control plane will render
date: 2026-09-17
kind: finding
status: open
category: refactor
tags: statusline, control-plane, ports, osc8, duplication
---

## Summary
`lib/runtime/scripts/statusline.sh` carries ~70 lines that discover listening
ports with `ss`, look their labels up in `devcontainer.json`
`portsAttributes` (falling back to `.cspace.json` `container.ports`), curate
unlabeled ports out when the project labeled any, and wrap each label in an
OSC 8 hyperlink to `http://<sandbox>.<project>.cspace.test:<port>/`. The
2026-09-17 control-plane design gives the same rule to `control.Ports()` and
renders it in the sidebar with lipgloss's `Hyperlink`. Once that lands the
logic exists twice, in two languages, on two release cadences.

## Details
The statusline runs inside the sandbox, once per assistant message, and is
the only place that surfaces ports to an *interactive* session — so it cannot
simply be deleted when the control plane arrives. What it can stop doing is
the fragile half: the OSC 8 wrapping exists because the visible URL was too
long for the bar, and two earlier attempts at shorter text were reverted
because some renderers strip OSC 8 (hence the `CSPACE_STATUSLINE_PORT_URLS=1`
escape hatch and the explicit test asserting the escape bytes).

Proposed after rollout step 3 lands, none chosen:

1. Keep the discovery and curation, drop the OSC 8 wrapping back to plain
   text — the control plane's sidebar becomes the clickable surface, and the
   statusline goes back to being a status *line*.
2. Have the statusline ask cspace instead of re-deriving: the in-sandbox
   `cspace` binary is already on PATH, so a `--json` control query would make
   the label/curation rule single-sourced in Go.
3. Leave it alone and accept the duplication, with a comment in each place
   pointing at the other.

(2) is the only one that actually removes the second implementation, but it
adds a per-message subprocess to a script that runs on every assistant
message — measure before choosing it.

## Updates
### 2026-09-17 — status: open
Filed from the control-plane design, which names this as a follow-up for
rollout step 3.

### 2026-09-18 — status: open
Rollout step 3's final review recorded a second, sibling duplication:
`cspace ports` (`internal/cli/cmd_ports.go`) and the dashboard's
`control.Ports` (`internal/control/ports.go`) are two independent
implementations of the same port-discovery/labeling rule this statusline
already duplicates, and they gave different answers for the same sandbox in
live verification. See
`.cspace/context/findings/2026-09-18-cspace-ports-and-control-ports-are-two-implementations.md`.
