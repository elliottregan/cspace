# Dashboard and interactive sessions

Run `cspace tui` for a dashboard of projects, containers, and sessions. The
sidebar is 24 columns wide, or 28 columns in terminals at least 100 columns wide.
Containers use the same planet symbols and colors as Claude’s statusline.

Click a session to attach or focus its pane. Click project/container headings
to collapse or expand them; arrow keys navigate and Enter activates the current
entry. Enter on a running container opens its default session. **New session** creates an independent Claude conversation in that
container. **New container** suggests the next available planet and accepts a
custom name, then opens the default Claude session after boot.

A container marked `✕` is stopped and cannot open a session. Select it and
press `u` to boot it through the existing `cspace up` flow, then open a session.
Boot re-provisions a stopped container; files outside its bind mounts are lost.
Enter on a stopped container displays this prerequisite instead of silently
collapsing an empty group.

The two-line header belongs to the active pane. It shows the container and
session, workspace branch and dirty state, the open pull request, and configured
`dev`/`preview` services. Running links accept ordinary mouse clicks. Stopped
services are dim; `?` means a listener probe failed. Pull request colors reflect
checks and merge state; `~` marks a retained result after a failed refresh.
GitHub results are cached per repository and branch for two minutes. Press `r`
from the sidebar for an explicit refresh.

Press `o` for the selected container’s details, or click **Details** in its
header. The dialog includes other services, resources, dependencies, session
names, supervisor state, and recent events. Selecting a project and pressing
`o`, or clicking the environment indicators at the sidebar bottom, shows
Browser, Daemon, and BuildKit details. The Browser indicator follows the active
project. Dialogs capture input: Escape closes them, arrows/Page Up/Page Down or
the wheel scroll, and Tab/Shift+Tab selects links and actions for Enter.

Click an error in the footer to read its full message in Environment Details.
If container discovery fails, the sidebar keeps its last successful snapshot;
Browser and BuildKit indicators become unknown while Daemon continues to show
its independently checked health. An XPC connection error usually means Apple
Container is unavailable: inspect `container system status`, and use
`container system start` when its service is stopped. The suggested new planet
comes from cspace's registry, so it can still appear while discovery is failing.

Every ordinary key in a focused pane goes to its program. The default leader is
Ctrl+Space; follow it with `h` for the sidebar, `n`/`p` for the next/previous pane,
`t` for the pane picker, `o` for the active container’s details, or `x` to close a
pane. The picker also provides sandbox shells, supervisor views, and host shells.
Press `?` in the sidebar (or leader `?`) for the complete key list. Bindings remain
configurable in `tui.keys` in `~/.cspace/config.json`, including `details`.

## Attach from the CLI

```sh
cspace attach mercury                         # reconnect to default Claude 1
cspace attach mercury --new                   # create independent Claude 2+
cspace attach mercury --session cspace-claude-2 # reconnect to that session only
```

`--new` and `--session` are mutually exclusive. Targeted attach fails if the
session disappeared; it never substitutes a new conversation. Session names are
shown in Details, and the dashboard discovers detached cspace-managed tmux
sessions when it opens and during background polling. There is no separate
session registry.

Closing a pane or quitting the dashboard detaches its client; tmux retains the
running conversation. Explicit additional sessions require tmux in the sandbox
image. Ordinary attach retains the older-image fallback and warns when its
session cannot survive a detach. Rebuild the image and recreate that sandbox to
use managed additional sessions on an older image.

If the dashboard quits during an unfinished container boot, it prints the
container’s name and recovery command. Boot cancellation/rollback, stopping
servers, renaming sessions, browsing conversation history, and automatically
resuming conversations after container recreation are outside this UI change.
Claude’s own statusline remains enabled.
