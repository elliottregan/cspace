---
title: Attach client-record .tmp files from a crashed write are never reaped
date: 2026-09-19
kind: finding
status: open
category: bug
tags: control-plane, attach, tmux, sweep, cleanup
---

## Summary
`Attachment.writeRecord` (`internal/control/attach.go:220-238`) publishes a
client record atomically — write `<name>.json.tmp`, then rename it over
`<name>.json` — and removes the temporary file only when the *rename*
fails. A crash, a SIGKILL or a panic between `os.WriteFile` and
`os.Rename` leaves the `.tmp` behind forever: the startup sweep filters the
directory by the `.json` suffix, so it never looks at one, and nothing else
in cspace reads or deletes it. One file accumulates per crash per pane
under `~/.cspace/controlplane/<project>/<sandbox>/`.

## Details
```go
tmp := path + ".tmp"
if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
    return
}
if err := os.Rename(tmp, path); err != nil {
    _ = os.Remove(tmp)   // the only cleanup there is
}
```

The litter is inert: the file is a valid record body but under a name the
sweep's `.json` filter skips, so it is never parsed, never counted, and
never mistaken for a live client. The cost is disk and directory noise, and
a person reading the directory by hand seeing records that describe nothing.

Proposed fix: in the sweep's directory walk, unlink any `*.json.tmp` whose
mtime is older than a few minutes. The age bound is what keeps it from
racing a `writeRecord` that is in flight right now — the write and rename
are microseconds apart, so anything minutes old is by definition abandoned.
The sweep already holds the sandbox's `attach.lock` while it walks, so the
reap costs nothing extra in locking.

Alternatively, `writeRecord` could `defer os.Remove(tmp)` unconditionally
(a successful rename makes the remove a harmless ENOENT), which closes the
panic case but not the SIGKILL one. The sweep-side reap covers both.

## Updates
### 2026-09-19 — status: open
Filed from the plan 4b whole-branch review's parked-findings adjudication
(Process: "Orphaned `<record>.json.tmp` from a crashed `writeRecord` is
never reaped" — ruled FILE).
