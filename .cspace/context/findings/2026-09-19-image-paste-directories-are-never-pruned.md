---
title: Image-paste directories are never pruned
date: 2026-09-19
kind: finding
status: open
category: observation
tags: control-plane, dashboard, paste, clipboard, retention, disk
---

## Summary
Leader `v` writes the clipboard's PNG to disk and types its path into a
pane. Nothing ever deletes those files. A sandbox pane's images land in
`~/.cspace/sessions/<project>/<sandbox>/paste/`, which survives until
`cspace down` wipes the whole session directory — so a sandbox that is
never torn down accumulates them for as long as it lives. A **host shell**
pane's images land in `~/.cspace/paste/`, which belongs to no sandbox and
is therefore never wiped by anything at all.

Verified live on 2026-09-19 (plan 5, Task 7): four pastes left four files,
three under the sandbox's session directory and one under `~/.cspace/paste`.
`cspace down mouse-smoke` removed the three with the session directory and
left `~/.cspace/paste/20260919-110652.559.png` behind. It was removed by
hand afterwards.

## Details
`osaClipboard.pasteDirs` (`internal/cli/clipboard.go`) picks the directory:

- `project`/`sandbox` set → `control.SessionDir(home, project, sandbox)/paste`,
  which the sandbox sees through its `/sessions` bind mount.
- both empty (a host shell) → `~/.cspace/paste`, typed as a host path.

The only deletion in the whole path is `discardPaste`
(`internal/controlplane/clipboard.go`), and it removes exactly one file:
the PNG of a paste that could not be delivered, because its tab closed or
its pane exited while `osascript` ran. A *delivered* paste is kept
deliberately — the pane was just told where to find it — and nothing
revisits it later.

Sizes are whatever was on the pasteboard: a full-screen screenshot on a
Retina display is routinely 3–8 MB, and an agent debugging a UI can paste
dozens in a session. The files are `0644` inside a `0700` directory, under
the person's own home, so this is a disk-retention question rather than a
security one.

Worth deciding before it bites:

- a cap per directory (keep the newest N, or the last 7 days), applied on
  write, which is the only moment cspace is already in that directory;
- `cspace doctor` reporting the total, so it is at least visible;
- or an explicit decision that these are the operator's files to manage,
  documented where `v` is documented.

`~/.cspace/paste` is the sharper edge of the two: nothing in cspace's
lifecycle — not `down`, not `daemon stop` — ever looks at it.

## Updates
- 2026-09-19: filed from plan 5 Task 7's live verification. Raised during
  Task 4's review and parked for the branch review; this entry is the
  measured version of it.
