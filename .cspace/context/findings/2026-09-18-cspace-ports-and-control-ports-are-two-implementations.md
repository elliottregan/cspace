---
title: "cspace ports" and the dashboard's control.Ports are two separate implementations that disagree
date: 2026-09-18
kind: finding
status: open
category: refactor
tags: control-plane, ports, duplication, cli
---

## Summary
`cspace ports` (`internal/cli/cmd_ports.go`) and `control.Ports`
(`internal/control/ports.go`), used by the dashboard, compute a sandbox's
port list independently, with different discovery, labeling and curation
rules — and gave different answers for the same sandbox in Task 9's live
verification run. This is one instance of a wider pattern on this branch:
several CLI commands still carry their own implementation of things
`internal/control` now owns, and two independent tmux driver instances
exist side by side. The dashboard's implementation is the better of the two
port implementations and is the one worth converging the CLI onto.

## Details
`cspace ports <sandbox>` (`internal/cli/cmd_ports.go`) TCP-probes a static,
hard-coded list of seven well-known ports (`portsToProbe`, `cmd_ports.go:31`:
3000, 4173, 5173, 6201, 8000, 8080, 8888) directly from the host with a
200ms timeout (`probePorts(entry.IP, portsToProbe, 200*time.Millisecond)`,
`cmd_ports.go:54`), and labels them from a hard-coded map (`portLabel`,
`cmd_ports.go:19-27`) that includes `6201: "cspace control"`.
`control.Ports` (`internal/control/ports.go:33-58`) instead execs `ss -tln`
*inside* the sandbox (`c.containers.Exec(..., []string{"ss", "-tln"}, ...)`,
`ports.go:44`), labels from the project's own
`devcontainer.json`/`.cspace.json` (`portLabelsFrom`, `ports.go:52`), and
explicitly hides cspace's own plumbing via
`internalPorts = map[int]bool{6201: true, 53: true}` (`ports.go:26-28`, the
supervisor control port and the in-sandbox dnsmasq forwarder).

Task 9's live run against the same sandbox with a listener on port 5173
showed the two disagreeing exactly as the code predicts:

> `./bin/cspace-go ports dashcheck` → `5173 Vite dev server ...` and
> `6201 cspace control ...`; the dashboard's detail band showed `5173 ...`
> and `9222 ...` instead. ... "The two lists differ by design, and the
> dashboard's is the better one" — it finds ports nobody enumerated (9222,
> from the browser sidecar's CDP port, not in `cspace ports`'s static list),
> and it does not advertise cspace's own control port as something to visit.
> (`task-9-report.md`, Step 6 and Observation 1)

This is one case of a wider duplication the step-2 review of this same plan
already recorded: `cmd_send.go` builds its own `/send` HTTP request by hand
rather than calling `control.Client.Send` (`internal/cli/cmd_send.go:44-58`);
`cmd_agent.go`'s `runAgentStatus`/`runAgentInterrupt` likewise build their
own `/status`/`/interrupt` requests rather than calling
`control.Client.AgentStatus`/`Interrupt` (`internal/cli/cmd_agent.go:84-107`);
and `cmd_attach.go` maintains its own process-wide tmux driver,
`var defaultTmux = control.NewTmux()` (`cmd_attach.go:20`), which is a
second, independently-constructed `*control.Tmux` alongside the one every
`*control.Client` builds for itself and exposes via `Client.Tmux()`
(`internal/control/client.go:182,221`) — two tmux drivers that happen to
agree today only because both are freshly constructed from the same
`control.NewTmux()` constructor.

Worth converging `cspace ports` onto `control.Ports` (and, more broadly,
`cmd_send.go`/`cmd_agent.go` onto `control.Client`'s methods) in a later
step, so the CLI and the dashboard cannot silently disagree about a
sandbox's ports or state again.

This is the second implementation the 2026-09-17 statusline finding already
tracks a third of — see
`.cspace/context/findings/2026-09-17-statusline-port-links-duplicate-the-control-plane.md`,
updated to point here.

## Updates
### 2026-09-18 — status: open
Filed from the control-plane-3-dashboard final review's Task 9 observations
(`cspace ports` and `control.Ports` disagree) merged with the step-2 review's
carried-forward duplication note (`cmd_send`/`cmd_agent`/`cmd_attach` and two
tmux drivers).
