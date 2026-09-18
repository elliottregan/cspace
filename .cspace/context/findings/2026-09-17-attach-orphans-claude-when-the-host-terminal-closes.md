---
title: exec'd processes survive their host terminal closing, so cspace attach orphans claude
date: 2026-09-17
kind: finding
status: open
category: bug
tags: attach, apple-container, tmux, lifecycle, control-plane
---

## Summary
`cspace attach` replaces itself with `container exec -it <container> claude
--dangerously-skip-permissions` via `syscall.Exec`. Nothing on the host then
stands between the terminal and the substrate — which is exactly why it was
written that way, and exactly why closing the window leaks. A process started
by `container exec -it` does not die when its host-side terminal goes away:
probed on 2026-09-17, an exec'd process was still alive 30 s after its host
terminal closed. Every closed attach window leaves a `claude` running inside
the sandbox, holding its context and its credential, until `cspace down`.

## Details
Two separate deaths are involved and neither reaches the guest:

- **The host-side client.** `syscall.Exec` means the `container` CLI *is* the
  cspace process; when the terminal is destroyed the process gets SIGHUP and
  dies, and no Go code is left to run anything afterwards. There is no seam
  for cleanup because the process that would do it was replaced.
- **The guest-side payload.** Apple Container does not tear the exec'd
  process down when its client disconnects. With tmux in the picture the same
  is true one level up: killing the host-side `container exec` leaves the
  guest tmux client attached indefinitely, and `tmux detach-client -t <tty>`
  from a fresh `container exec` reaps it at once.

So the fix has two halves, both in the 2026-09-17 control-plane design:
run the exec as a foreground *child* so there is a process left alive to do
the cleanup, and make that cleanup an explicit `tmux detach-client` against
the client this attach created — identified as the one new tty between a
`list-clients` before the attach and one after, under a per-sandbox flock.

Before tmux exists in the image there is no recovery at all for the
already-orphaned `claude`: nothing inside the sandbox knows the client is
gone. That is the fallback path, and it warns.

## Updates
### 2026-09-17 — status: open
Filed from the control-plane design's substrate probes.
