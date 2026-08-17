---
title: Credential warnings emitted after overlay.Start are shredded mid-render
date: 2026-08-17
kind: finding
status: resolved
category: bug
tags: overlay, cmd-up, credentials, ux
---

## Summary
The env_file collision warning was written to raw stderr at `cmd_up.go:366`,
after `overlay.Start()` at line 222. The overlay redirects stdout into a
pending buffer and bubbletea owns the terminal, so the warning raced the
render and arrived as unreadable fragments that vanished on the next frame.

The file's own comment at lines 107-112 documents exactly this hazard and is
why the Anthropic auth warnings were deliberately hoisted above the overlay.
The env_file warning could not be hoisted the same way, because it depended on
the compose env_file merge at line 349.

The cost was not cosmetic: at the boot that created `mercury`, this warning
correctly reported that the project's `.env` was overriding `GH_TOKEN`,
`GITHUB_PERSONAL_ACCESS_TOKEN`, and `CLAUDE_CODE_OAUTH_TOKEN`. It was
unreadable, and the resulting 401 was later misdiagnosed as a host `gh`
re-authentication.

## Updates
### 2026-08-17 — @agent — status: resolved
cspace's credential resolution no longer depends on the compose env_file
merge, so all of it — resolve, verify, select, and the summary line — now runs
before `overlay.Start` and prints to the real terminal. No overlay surface was
added; the dependency that forced the late write is gone.
