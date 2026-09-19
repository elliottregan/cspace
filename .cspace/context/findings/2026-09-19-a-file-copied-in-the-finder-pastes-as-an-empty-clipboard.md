---
title: A file copied in the Finder pastes as "the clipboard is empty"
date: 2026-09-19
kind: finding
status: open
category: observation
tags: control-plane, dashboard, paste, clipboard, macos
---

## Summary
Copying an image *file* rather than an image puts a file reference on the
pasteboard — `«class furl»` — and no `«class PNGf»` and no text. Leader `v`
therefore probes, finds no image, falls back to the text paste, finds that
empty too, and reports **`paste image: the clipboard is empty`** while the
pasteboard is holding a perfectly good PNG the operator just copied.

Nothing is typed into the pane and nothing is written to disk, so this is
a misleading message rather than a broken paste — but "empty" is the one
thing the clipboard demonstrably is not, and the obvious next move (copy
the screenshot file, press `v`) is exactly the one that hits it.

## Details
Measured on this Mac, 2026-09-19:

```
$ osascript -e 'set the clipboard to (POSIX file "/tmp/finder.png")'
$ osascript -e 'clipboard info'
«class furl», 126
$ pbpaste | wc -c
       0
```

Against the same pasteboard, leader `v` in a Claude pane left the input box
untouched and put `paste image: the clipboard is empty` in the footer
(plan 5, Task 7, capture `extra.txt` step B).

`osaClipboard.hasImage` (`internal/cli/clipboard.go`) tests for
`«class PNGf»` in `clipboard info`, which is the right test for an image on
the pasteboard and says nothing about a reference to one. Two candidate
answers, both small:

1. **Read the reference.** When `clipboard info` reports `«class furl»`,
   resolve it (`the clipboard as «class furl»` gives an HFS path;
   `POSIX path of` converts it) and, if it names a file whose bytes start
   `\x89PNG`, use that path directly — no copy into `paste/` is needed for
   a sandbox pane only if the file is already visible inside it, which it
   usually is not, so the honest version still copies it in.
2. **Say what is actually there.** Keep the refusal, but distinguish
   "the clipboard holds a file, not an image — copy the image itself"
   from "the clipboard is empty". Cheap, and it turns a dead end into an
   instruction.

Note that this is the AppleScript `POSIX file` form of the flavour. A real
Finder Copy may carry additional flavours (a display name as text, for
instance), in which case the text fallback would type *that* into the pane
instead — worth confirming by hand before choosing an answer.

## Updates
- 2026-09-19: filed from plan 5 Task 7's live verification, which was asked
  to observe exactly this case. Raised as a candidate during Task 4's
  review.
