# Apple Container 1.5.0 fixtures

Captured on 2026-09-29 from a running 1.5.0 client and server on macOS arm64.
The only container present was buildkit. These are read-only captures from
`container system status`, `container ls --all --format json`,
`container inspect buildkit`, `container network inspect default`, and
`container stats --no-stream --format json`.

JSON was filtered to explicit field allowlists before printing or saving;
container environment, mounts, and other unrelated fields were never captured.
The system-status home directory was replaced with `/Users/dev`.
The stopped system-status test is derived from the captured running table;
it is not a capture of an actual stopped service.

These fixtures verify parsing only. They do not establish runtime compatibility
for image builds, exec/PTY, mounts, DNS, or container lifecycle.

## Local runtime validation

On 2026-09-29, both client and server were 1.5.0 on macOS arm64. The
installed cspace was 1.0.0-rc.52. Tests used a disposable `compat15` project,
separate image tags, and its own sandbox, browser, backend, and named volume.

Verified on the real runtime:

- Alpine create, exec, address discovery, list, stop, and removal through the
  Go substrate adapter.
- Image builds through both the source checkout and the installed Homebrew
  binary's temporary asset extraction/download path.
- Image assets (tmux, supervisor, hook scripts, and tmux config), plus image
  version-label parsing.
- `cspace up` with a compose backend and shared browser; authenticated
  supervisor status reports idle without sending a model prompt.
- Workspace bind-mount writes visible on the host, named-volume writes as
  the `dev` user, and a writable tmpfs mount.
- `route_localnet=1` from the kernel argument, the inbound DNAT rule, and
  host access to dev/preview servers bound only to guest loopback.
- Host and sandbox DNS for the compose backend, browser-to-workspace DNS,
  external DNS/HTTPS, and the sandbox's CDP loopback relay.
- Real Chromium page loads through CDP and Playwright's run-server.
- Browser and backend restarts onto new IPs, followed by successful access
  from the existing sandbox using the original DNS names/relay.
- `--init` signal forwarding: PID 1 was `.cz-init`, the child received
  SIGTERM and wrote a host-visible acknowledgment, then ordinary deletion
  succeeded without force.
- The Bash statusline suite inside the image (macOS Bash 3 skips it).

`/proc/sys` remains read-only on 1.5. Keep the kernel argument and DNAT
readback guard. A missing container still exits nonzero with
`Error: container not found: <name>`.

Session and dashboard checks also passed against real Claude, without
submitting any task prompts:

- Default attach/reconnect and targeted reconnect preserve the same process.
- Sequential and simultaneous `attach --new` calls create independent
  processes, identities, and `CSPACE_AGENT_STATE_FILE` values.
- Legacy and numbered hook files report separate session states.
- Missing-session attach fails without creating a replacement.
- CLI/TUI resizing reaches tmux; the sidebar creates and discovers sessions.
- Closing panes, closing terminals, quitting, and reopening the TUI preserve
  the running sessions. No clients or client records remained after detach.
- Host SIGTERM now completes the attach cleanup in about two seconds while
  preserving the underlying Claude process.
- A second sandbox booted through the normal automatic clone flow. Its shell
  accepted input and its header showed the branch and running dev/preview
  services at 140, 100, and 80 columns.

Live testing found three cspace issues, fixed in this compatibility pass:
non-UTF-8 tmux output replaced tab separators with underscores; session
creation used an unsupported `=name` target for tmux option commands; host
SIGTERM could leave attach waiting on the container exec client indefinitely.
See the 2026-09-29 session-discovery, session-creation-targets, and
attach-sigterm findings under `.cspace/context/findings/`. The tmux findings
were not established as changes in Apple Container 1.5.

After the fixes, `make check` and `make test-race` passed locally. The stock
macOS Bash 3 skip in `make check` was covered by running the statusline suite
with Bash inside the test sandbox. No CI workflow changes were required.

Cleanup also verified the shared browser lifecycle: after removing Mercury,
Venus and both browser endpoints remained reachable. Removing the last
sandbox removed the shared browser. All test sandboxes, backend sidecars,
named volumes, session state, auto-provisioned clone, and test image tags
were then removed; the pre-existing BuildKit service and unrelated volumes
were retained.
