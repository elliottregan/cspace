---
title: pasteDirs' "half a name is still a sandbox" branch is unreachable through Image
date: 2026-09-19
kind: finding
status: open
category: observation
tags: control-plane, cli, clipboard, paste, tests
---

## Summary
`osaClipboard.pasteDirs` (`internal/cli/clipboard.go`) has a fallback for
"one of project/sandbox is empty, the other is not" — the comment calls it
"half a name is still a sandbox." `Image`, `pasteDirs`' only production
caller, now rejects an empty project half (`validatePathSegment`, added by
the fix for Important 1 in the same review) and an empty sandbox half
(`validateSandboxName`, which rejects `""` because `sandboxNamePattern`
requires at least one character) before `pasteDirs` is ever reached — so
project and sandbox arrive there either both empty (host shell) or both
set (a sandbox pane). The half-a-name branch is dead code from `Image`'s
side.

## Details
`pasteDirs`, `internal/cli/clipboard.go:145-152`:
```go
func (c *osaClipboard) pasteDirs(project, sandbox string) (hostDir, paneDir string) {
	if project == "" && sandbox == "" {
		d := filepath.Join(c.home, ".cspace", "paste")
		return d, d
	}
	return filepath.Join(control.SessionDir(c.home, project, sandbox), "paste"), "/sessions/paste"
}
```
`TestPasteDirsPutEachCaseWhereItsPaneCanReadIt`
(`internal/cli/clipboard_test.go:386-420`) pins two subtests —
`"half a name is still a sandbox"` and `"half a name is still a sandbox,
the other half"` — that call `pasteDirs` directly with one half empty, as
if that were a state `Image` could still produce. It cannot:
`TestClipboardImageRejectsANameItWouldJoinIntoAPath`'s `"an empty half"`
subtest already pinned that an empty project is rejected before
`hasImage`/`pasteDirs` run, and the same fix wave's
`validatePathSegment` (`internal/cli/clipboard.go:110-120`) generalized
that rejection to cover the project half by shape, not just by name.

This was already true before this branch review's fix wave — the
"an empty half" test predates it — but it is worth recording precisely
because the branch review's Important 1 fix widened `Image`'s guard
(project half now goes through `validatePathSegment` instead of
`validateSandboxName`), which is exactly the kind of change that could
have re-opened this branch if the new check had been narrower than the
old one. A one-line comment on `pasteDirs`
(`internal/cli/clipboard.go:137-144`, added in this fix wave) now says so
directly at the code; this finding is the lasting record of *why*, for
whoever next touches either `Image`'s validation or `pasteDirs` itself and
needs to know the branch's tests describe an unreachable state, not a live
one.

**Failing scenario.** None reachable through `Image` today. The two
`pasteDirs` subtests above exercise the branch directly and would need to
be dropped, or `pasteDirs` itself trimmed, if this precondition is ever
formalized further (e.g. `pasteDirs` taking a single validated pair type
instead of two strings).

Source:
`.superpowers/sdd/2026-09-19-control-plane-5-mouse-and-image-paste/review-branch.md`,
Minor 8 / adjudication row T4-f ("`pasteDirs`' 'half a name is still a
sandbox' branch is unreachable" — ruled **FILE** (or fix with a one-line
comment)) and Important 1's fix instructions ("The `an empty half` case is
what keeps `pasteDirs`' half-a-name branch unreachable through `Image` —
preserve it.").

## Updates
- 2026-09-19: filed from the plan 5 whole-branch review, during the fix
  wave (Important 1, Minor 1), alongside the one-line comment added to
  `pasteDirs` in the same wave's code commit.
