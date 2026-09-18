---
title: attachExec.Run's happy path has no hermetic test
date: 2026-09-18
kind: finding
status: open
category: observation
tags: control-plane, dashboard, attach, testing
---

## Summary
`attachExec.Run()` (`internal/cli/controlplane_actor.go:120-171`) is the
dashboard's attach path — tmux probe, `beginAttachOrWarn` bookkeeping,
`container exec` child, detach — and the final review calls it "the branch's
riskiest code." Its happy path has no automated test: `control.AttachArgv`
(`internal/control/argv.go:54-64`) resolves the container binary with
`exec.LookPath("container")` (`argv.go:61`), which succeeds on any machine
with Apple Container installed and would make a real `Run()` shell out to
the live substrate. The only verification this path has ever received is
Task 9's manual pty-driven run against a real sandbox.

## Details
`internal/cli/controlplane_actor_test.go:227-234` documents the gap
directly: `TestAttachRunErrTreatsANonZeroExitAsNormalReturn` deliberately
exercises `attachRunErr` against a trivial `sh -c "exit 3"` child rather than
running `attachCommand(row).Run()`, with the comment "AttachArgv resolves
'container' via exec.LookPath, which is on this machine's PATH, so a real
Run() would shell out to the actual Apple Container CLI — exactly the real
I/O internal/cli's tests must not do." Every other `attachExec` test in that
file exercises `attachResult`/`attachRunErr` in isolation, or guards
`Run()`'s empty-container early return (`TestControlPlaneActorAttachGuardsAnEmptyContainer`) — none drives the
probe → `beginAttachOrWarn` → child → detach sequence end to end.

That sequence was verified live exactly once, in Task 9's report
(`.superpowers/sdd/2026-09-18-control-plane-3-dashboard/task-9-report.md`,
Step 10): the bookkeeping half passed, but a separate, still-open rendering
defect was found there
(`.cspace/context/findings/2026-09-18-dashboard-screen-comes-back-blank-after-attach.md`).
A regression in the probe/bookkeeping/child/detach sequence itself would
currently only be caught by another such manual run.

Fix proposed by the review: give `AttachArgv` (or `attachExec.Run`) an
injectable `lookPath`-style seam — e.g. a func field defaulting to
`exec.LookPath`, or an interface `Run` takes as a parameter — so a test
double can stand in for the real `container` binary and the full
probe → attach → child → close path can run hermetically in `go test`.

## Updates
### 2026-09-18 — status: open
Filed from the control-plane-3-dashboard final review's parked-findings
adjudication (Task 7: attach's happy path is untested; a lookPath seam would
make it hermetic).
