---
title: cspace down race: stop+rm sequence leaves container behind, blocks next up with "already exists"
date: 2026-05-03
kind: finding
status: resolved
category: bug
tags: substrate, applecontainer, rc-blocker
---

## Summary
After `cspace down sgd-1`, the next `cspace up sgd-1` fails immediately with `Error: container with id cspace-space-game-demo-sgd-1 already exists`. The substrate adapter's Stop() runs `container stop` then `container rm` and ignores both errors; in practice the second command races with the first and the container is not actually removed. Manual `container stop && container rm` works, so the underlying CLI is fine — it's our sequencing that's wrong.

## Details
Reproduction (cspace v1.0.0-rc.2):

```
cspace down sgd-1                   # exits 0, prints "sandbox sgd-1 down"
cspace up sgd-1                     # fails: "container ... already exists"
container list -a | grep sgd-1      # container still listed as running
container stop cspace-...-sgd-1     # OK
container rm   cspace-...-sgd-1     # OK
cspace up sgd-1                     # works
```

Code site: `internal/substrate/applecontainer/adapter.go` Stop():
```go
_ = exec.CommandContext(ctx, "container", "stop", name).Run()
_ = exec.CommandContext(ctx, "container", "rm", name).Run()
return nil
```

Both errors are deliberately swallowed (idempotent intent), but that's hiding a real failure mode where `rm` runs before `stop` has actually settled the container. Three things to consider:

1. Don't ignore stderr — capture it and either log or surface it. The current code makes this class of bug invisible.
2. `container rm --force` would collapse stop+rm into one call and avoid the race. Worth checking whether Apple Container 0.12.x supports it.
3. If we keep two commands, wait for the first to actually transition the container to stopped (poll `container inspect`) before issuing rm.

Impact: every cycle of `cspace down` + `cspace up <same-name>` is broken on the rc — testers will hit this immediately. This is the kind of paper-cut that erodes trust in the rc, so it's worth a fast follow-up before v1.0.0.

## Updates
### 2026-05-03T17:50:47Z — @agent — status: open
filed

### 2026-05-03T17:56:45Z — @agent — status: resolved
Resolved by 481f647 — Stop() now uses `container rm --force` for atomic stop-and-remove and captures stderr so future substrate failures aren't swallowed. Verified end-to-end against the live sgd-1 sandbox: `cspace down sgd-1 && cspace up sgd-1` cycles cleanly without manual `container rm` cleanup. The "container not found" path still returns nil so idempotency is preserved.
