# Control plane step 1 — sandbox side Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Put tmux, a tmux config, an agent-state hook script and its `~/.claude/settings.json` wiring into the sandbox image, and turn `cspace attach` into a foreground child that attaches to a persistent tmux session and explicitly detaches its client on exit — so a closed window stops orphaning `claude` inside the sandbox.

**Architecture:** Three layers of change, bottom-up. (1) The **image**: `lib/runtime/tmux.conf` and `lib/runtime/scripts/cspace-agent-state.sh` ship via per-file Dockerfile COPYs, `tmux` is added to the apt set, and `make sync-embedded` learns the new non-`.sh` file. (2) The **entrypoint**: the settings seed it writes every boot gains a `hooks` block whose nine entries all run `cspace-agent-state.sh <state>`, which atomically writes `/sessions/agent-state.json` — the host side of the same bind mount. (3) A new **`internal/control`** package, the first slice of the control API the rest of the rollout extracts into: the attach argv (with the TERM/COLORTERM mapping moved out of `internal/cli/cmd_attach.go`), the tmux presence probe, `tmux list-clients` / `detach-client`, and the flock'd attach-lock + client-record bookkeeping. `cspace attach` then stops `syscall.Exec`ing: it runs `container exec` as a child, forwards signals, propagates the exit status, and detaches its tmux client on the way out.

**Tech Stack:** Go 1.26 (stdlib only — no new module dependencies in this step), Cobra, bash (Debian bookworm in-image), tmux 3.3a, Apple Container's `container` CLI, jq.

**Spec:** `docs/superpowers/specs/2026-09-17-control-plane-design.md` — rollout step 1, "Sandbox side" (Image, Sessions, Detach protocol as it applies to `cspace attach`, Agent state hooks), plus the two findings its closing section says to file and the no-tmux fallback from "Error handling".

**Out of scope (later rollout steps, do not build here):** `internal/pane`, `internal/controlplane`, the bubbletea v2 dashboard, `Snapshot`/`AgentStatus`/`InteractiveState`/`Ports`/`Events` queries, the `Down`/`Send`/`Interrupt`/`RestartBrowser`/`Up` actions, and the control-plane **startup sweep** of stale client records (spec's detach-protocol item 4 — it runs at control-plane startup, which does not exist yet). Step 1 writes the record files the sweep will later read; a stale record is an inert JSON file, never a lock.

## Global Constraints

Exact values, copied from the spec and from CLAUDE.md. Every task's requirements implicitly include this section.

- **tmux config lines are verified-as-accepted text. Copy them byte-for-byte.** tmux in the image is 3.3a (Debian bookworm). `extended-keys` MUST be `always` — with `on`, Shift+Enter in kitty CSI-u form is silently dropped, not even downgraded. `extended-keys-format` MUST NOT appear as a directive: tmux 3.3a rejects it as an invalid option. (The config's header comment does name it, to say why it is absent — the drift test in Task 2 matches directive lines only, not prose.)
- **Apple Container's builder does not recurse directory COPYs.** Every file added under `lib/runtime/` needs its own `COPY` line in `lib/templates/Dockerfile` (or a glob that covers it) or it silently never lands in the image.
- **`internal/assets/embedded/` is gitignored and generated.** Always build and test through `make` (`make build`, `make test`, `make check`) or run `make sync-embedded` first. A bare `go build` on a clean checkout embeds an empty tree.
- **No new Go module dependencies in this step.** `charm.land/bubbletea/v2`, `charmbracelet/x/vt` and `creack/pty` belong to steps 3 and 4. `go.mod` must be unchanged when this plan is done.
- **`make check` must be green at the end of every task** — it is `fmt-check vet lint test test-scripts`. `make lint` runs `shellcheck lib/runtime/scripts/*.sh scripts/*.sh`, which includes the `*.test.sh` files, so new bash must be shellcheck-clean.
- **Run `make fmt` on touched Go files before `make check`.** The Go in this plan is written for reading, not byte-aligned to gofmt (trailing-comment columns especially), and `fmt-check` fails on a single misaligned comment.
- **A hook must never exit non-zero.** A `PreToolUse` hook exiting 2 blocks the tool call. `cspace-agent-state.sh` ends in `exit 0` on every path.
- **`cspace attach` keeps `--dangerously-skip-permissions`** and keeps writing `\033c` to stdout before handing over (the reset is skipped when attach has printed a warning, so the warning stays readable).
- The state file is `/sessions/agent-state.json` inside the sandbox, which is `~/.cspace/sessions/<project>/<sandbox>/agent-state.json` on the host (existing bind mount, no new mounts).
- Attach bookkeeping lives at `~/.cspace/controlplane/<project>/<sandbox>/` — `attach.lock` (flock) and one `<session>.<tty>.json` record per live client.
- Commit messages: short imperative sentences, e.g. "Fix EPIPE crash in supervisor and $DC reference in cmd_up". When a commit resolves a finding, append `(cs-finding:<slug>)`.
- Do not run `cspace up` as part of Tasks 1–8. Task 9 is the only task that touches a live sandbox, and it is run by a human on a Mac with Apple Container.

## File Structure

**Created**

| File | Responsibility |
|---|---|
| `.cspace/context/findings/2026-09-17-attach-orphans-claude-when-the-host-terminal-closes.md` | The orphan bug this step fixes. Filed open, resolved by Task 8. |
| `.cspace/context/findings/2026-09-17-statusline-port-links-duplicate-the-control-plane.md` | The statusline/OSC-8 duplication, for step 3. |
| `lib/runtime/tmux.conf` | The only tmux config any cspace session ever loads (`-f`, so `~/.tmux.conf` is ignored). |
| `lib/runtime/scripts/cspace-agent-state.sh` | Hook target: writes `/sessions/agent-state.json` atomically. |
| `lib/runtime/scripts/cspace-agent-state.test.sh` | Bash test for the above, run by `make test-scripts`. |
| `lib/runtime/scripts/cspace-entrypoint.test.sh` | Bash test that renders the entrypoint's settings heredoc and asserts the hooks block with jq. |
| `internal/control/control.go` | Package doc, session/path constants, `ControlPlaneDir`. |
| `internal/control/argv.go` | `AttachSpec`, `ClaudeAttach`, `AttachArgv`, and the TERM/COLORTERM mapping moved out of the CLI. |
| `internal/control/argv_test.go` | Golden argv tables + the moved terminal-env tests. |
| `internal/control/tmux.go` | `Execer`/`CLIExecer` seam, `Tmux.Present` (memoized probe), `ListClients`, `DetachClient`. |
| `internal/control/tmux_test.go` | Fake-`Execer` tests; defines the `fakeExec` helper reused by `attach_test.go`. |
| `internal/control/attach.go` | Attach lock, client records, `BeginAttach` / `Attachment.Close`. |
| `internal/control/attach_test.go` | Tempdir + fake-`Execer` tests for the record/detach lifecycle. |
| `internal/cli/errors.go` | `ExitError`, so a child's exit status becomes cspace's without a printed "Error:". |

**Modified**

| File | Change |
|---|---|
| `lib/templates/Dockerfile` | `tmux` in the apt set; COPY `tmux.conf` and `cspace-agent-state.sh`; `RUN test -f` guards. |
| `Makefile` | `sync-embedded` copies `lib/runtime/tmux.conf` (no existing glob covers `lib/runtime/*.conf`). |
| `lib/runtime/scripts/cspace-entrypoint.sh` | `hooks_block` built above the settings heredoc and interpolated into it. |
| `internal/assets/assets_test.go` | Drift guards: the embedded tree carries `runtime/tmux.conf`; every embedded runtime file has a Dockerfile COPY; the Dockerfile installs tmux. |
| `internal/cli/cmd_attach.go` | Foreground child + signal forwarding + detach; `--no-tmux`; terminal-env helpers deleted (moved to `internal/control`). |
| `internal/cli/cmd_attach_test.go` | Deleted in Task 5 (every test in it moves to `internal/control/argv_test.go`), recreated in Task 8 for the child runner. |
| `internal/cli/cmd_up.go` | `applyTerminalEnv` → `control.ApplyTerminalEnv`; the auto-attach call gets the new signature. |
| `internal/cli/tui_actor.go` | Attach through `control.AttachArgv` + `BeginAttach`/`Close` so the v1 dashboard shares the tmux session (this file is deleted in rollout step 3). |
| `cmd/cspace/main.go` | Honor `cli.ExitError`. |

---

### Task 1: File the two findings

The spec's closing section names two findings to file. Task 8's commit resolves the first, so it has to exist first.

**Files:**
- Create: `.cspace/context/findings/2026-09-17-attach-orphans-claude-when-the-host-terminal-closes.md`
- Create: `.cspace/context/findings/2026-09-17-statusline-port-links-duplicate-the-control-plane.md`

**Interfaces:**
- Consumes: nothing.
- Produces: the slug `2026-09-17-attach-orphans-claude-when-the-host-terminal-closes`, referenced by Task 8's commit message and by a comment in `internal/cli/cmd_attach.go`.

- [ ] **Step 1: Write the orphan finding**

Create `.cspace/context/findings/2026-09-17-attach-orphans-claude-when-the-host-terminal-closes.md`:

```markdown
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
```

- [ ] **Step 2: Write the statusline finding**

Create `.cspace/context/findings/2026-09-17-statusline-port-links-duplicate-the-control-plane.md`:

```markdown
---
title: statusline port links duplicate what the control plane will render
date: 2026-09-17
kind: finding
status: open
category: refactor
tags: statusline, control-plane, ports, osc8, duplication
---

## Summary
`lib/runtime/scripts/statusline.sh` carries ~70 lines that discover listening
ports with `ss`, look their labels up in `devcontainer.json`
`portsAttributes` (falling back to `.cspace.json` `container.ports`), curate
unlabeled ports out when the project labeled any, and wrap each label in an
OSC 8 hyperlink to `http://<sandbox>.<project>.cspace.test:<port>/`. The
2026-09-17 control-plane design gives the same rule to `control.Ports()` and
renders it in the sidebar with lipgloss's `Hyperlink`. Once that lands the
logic exists twice, in two languages, on two release cadences.

## Details
The statusline runs inside the sandbox, once per assistant message, and is
the only place that surfaces ports to an *interactive* session — so it cannot
simply be deleted when the control plane arrives. What it can stop doing is
the fragile half: the OSC 8 wrapping exists because the visible URL was too
long for the bar, and two earlier attempts at shorter text were reverted
because some renderers strip OSC 8 (hence the `CSPACE_STATUSLINE_PORT_URLS=1`
escape hatch and the explicit test asserting the escape bytes).

Proposed after rollout step 3 lands, none chosen:

1. Keep the discovery and curation, drop the OSC 8 wrapping back to plain
   text — the control plane's sidebar becomes the clickable surface, and the
   statusline goes back to being a status *line*.
2. Have the statusline ask cspace instead of re-deriving: the in-sandbox
   `cspace` binary is already on PATH, so a `--json` control query would make
   the label/curation rule single-sourced in Go.
3. Leave it alone and accept the duplication, with a comment in each place
   pointing at the other.

(2) is the only one that actually removes the second implementation, but it
adds a per-message subprocess to a script that runs on every assistant
message — measure before choosing it.

## Updates
### 2026-09-17 — status: open
Filed from the control-plane design, which names this as a follow-up for
rollout step 3.
```

- [ ] **Step 3: Verify the frontmatter matches the repo's format**

Run:
```bash
cd /Users/elliott/Projects/cspace && for f in .cspace/context/findings/2026-09-17-*.md; do
  echo "== $f"; sed -n '1,9p' "$f"; grep -cE '^## (Summary|Details|Updates)$' "$f"; done
```
Expected: both files print a frontmatter block whose keys are exactly `title`, `date`, `kind: finding`, `status: open`, `category`, `tags` — and a count of `3` for the required section headings.

- [ ] **Step 4: Commit**

```bash
cd /Users/elliott/Projects/cspace
git add .cspace/context/findings/2026-09-17-attach-orphans-claude-when-the-host-terminal-closes.md \
        .cspace/context/findings/2026-09-17-statusline-port-links-duplicate-the-control-plane.md
git commit -m "File the attach-orphan and statusline-duplication findings"
```

---

### Task 2: Ship tmux and its config in the sandbox image

**Files:**
- Create: `lib/runtime/tmux.conf`
- Modify: `Makefile` (the `sync-embedded` target, lines 16-34)
- Modify: `lib/templates/Dockerfile` (apt block around lines 55-79; runtime-script COPY block around lines 160-170)
- Test: `internal/assets/assets_test.go` (append)

**Interfaces:**
- Consumes: nothing.
- Produces: `/usr/local/etc/cspace-tmux.conf` inside the image (Task 5 names it as `control.TmuxConf`); `tmux` on the sandbox's PATH (Task 6 probes for it).

- [ ] **Step 1: Write the failing test for the embedded config**

Append to `internal/assets/assets_test.go` (the file already imports `encoding/json`, `os`, `path/filepath`, `testing`; add `io/fs`, `regexp` and `strings` to the import block — `path` comes with the tests in Step 5, adding it now would not compile):

```go
// TestEmbeddedRuntimeCarriesTmuxConf — tmux.conf is the first file under
// lib/runtime/ that is not a .sh, so no existing sync-embedded glob sweeps
// it. Without its own cp rule the embedded tree (and therefore any
// `cspace image build` outside a source checkout) silently loses it.
func TestEmbeddedRuntimeCarriesTmuxConf(t *testing.T) {
	runtimeFS, err := RuntimeFS()
	if err != nil {
		t.Fatalf("RuntimeFS() error: %v", err)
	}
	data, err := fs.ReadFile(runtimeFS, "tmux.conf")
	if err != nil {
		t.Fatalf("embedded runtime/tmux.conf missing: %v", err)
	}
	conf := string(data)

	// extended-keys=on silently swallows Shift+Enter in CSI-u form; only
	// `always` passes it through byte-for-byte. Verified on tmux 3.3a.
	if !strings.Contains(conf, "set -g extended-keys always") {
		t.Error("tmux.conf must set `extended-keys always`, not `on`")
	}
	// tmux 3.3a (bookworm) does not know extended-keys-format and rejects
	// the line, which would poison the whole config. Matched as a directive
	// on a non-comment line, not as a substring: the config's own header
	// comment names the option to explain why it is absent, so a substring
	// match would fail on that comment. `[^#\n]*` stops at the first `#`, so
	// neither a full-line comment nor a trailing one can trigger this.
	if regexp.MustCompile(`(?m)^[^#\n]*\bextended-keys-format\b`).MatchString(conf) {
		t.Error("tmux.conf sets extended-keys-format, which tmux 3.3a rejects as invalid")
	}
	// The host owns every key: a prefix would eat one of Claude's.
	if !strings.Contains(conf, "set -g prefix None") {
		t.Error("tmux.conf must unset the prefix so the host owns every key")
	}
	if !strings.Contains(conf, "set -g status off") {
		t.Error("tmux.conf must turn the status bar off")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd /Users/elliott/Projects/cspace && make sync-embedded && go test ./internal/assets/ -run TestEmbeddedRuntimeCarriesTmuxConf -v`
Expected: FAIL with `embedded runtime/tmux.conf missing: open tmux.conf: file does not exist`.

- [ ] **Step 3: Write the tmux config and the sync rule**

Create `lib/runtime/tmux.conf` — every line below was checked for acceptance on tmux 3.3a and the input-handling ones were verified behaviourally; do not reword them:

```
# cspace tmux config — loaded only via `tmux -f /usr/local/etc/cspace-tmux.conf`,
# so ~/.tmux.conf and /etc/tmux.conf are never read. tmux here is not a user
# tool: it is the thing that keeps a session alive when the host-side
# `container exec` dies, and nothing more. No key bindings of any kind.
#
# Verified on tmux 3.3a (Debian bookworm), which is what the sandbox image
# ships. `extended-keys-format` is deliberately absent: 3.3a rejects it as an
# invalid option, and the passthrough does not need it.

set -g extended-keys always      # "on" silently drops Shift+Enter (CSI-u)
set -g allow-passthrough on
set -s escape-time 0
set -g status off
set -g mouse off
set -g prefix None               # the host owns every key
set -g default-terminal tmux-256color
set -ga terminal-overrides ',xterm-256color:RGB'   # matches the outer TERM attach passes
set -g focus-events on
set -g history-limit 2000        # serves the reattach redraw only
```

In `Makefile`, inside `sync-embedded`, add the copy right after the runtime-scripts lines (after the `rm -f …/*.test.sh` line):

```makefile
	@cp lib/runtime/tmux.conf internal/assets/embedded/runtime/
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd /Users/elliott/Projects/cspace && make sync-embedded && go test ./internal/assets/ -run TestEmbeddedRuntimeCarriesTmuxConf -v`
Expected: PASS.

- [ ] **Step 5: Write the failing Dockerfile drift tests**

Append to `internal/assets/assets_test.go`, adding `path` to its import block (`regexp` came with Step 1):

```go
// TestDockerfileCopiesEveryEmbeddedRuntimeFile guards the per-file COPY trap
// (finding 2026-07-16-per-file-dockerfile-copy-and-gitignored-embedded-assets):
// Apple Container's builder drops the contents of a whole-directory COPY, so
// lib/templates/Dockerfile names runtime files one at a time. A file that
// reaches the build context without a COPY line produces no build error —
// just a missing file inside the sandbox at runtime.
func TestDockerfileCopiesEveryEmbeddedRuntimeFile(t *testing.T) {
	df, err := EmbeddedFS.ReadFile("embedded/templates/Dockerfile")
	if err != nil {
		t.Fatalf("embedded Dockerfile missing: %v", err)
	}

	// Collect every COPY source token: `COPY <src...> <dst>`, skipping
	// --from=/--chown= flags and the final destination argument.
	var sources []string
	for _, line := range strings.Split(string(df), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 3 || !strings.EqualFold(fields[0], "COPY") {
			continue
		}
		for _, tok := range fields[1 : len(fields)-1] {
			if strings.HasPrefix(tok, "--") {
				continue
			}
			sources = append(sources, tok)
		}
	}

	runtimeFS, err := RuntimeFS()
	if err != nil {
		t.Fatalf("RuntimeFS() error: %v", err)
	}
	err = fs.WalkDir(runtimeFS, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		want := "lib/runtime/" + p
		for _, src := range sources {
			if src == want {
				return nil
			}
			if ok, _ := path.Match(src, want); ok {
				return nil
			}
		}
		t.Errorf("no COPY line in lib/templates/Dockerfile ships %s — Apple Container's builder does not recurse directory COPYs, so it will be missing from the image", want)
		return nil
	})
	if err != nil {
		t.Fatalf("walking the embedded runtime tree: %v", err)
	}
}

// TestDockerfileInstallsTmux — the config is useless without the binary, and
// the binary is what `cspace attach` probes for before choosing the tmux argv.
func TestDockerfileInstallsTmux(t *testing.T) {
	df, err := EmbeddedFS.ReadFile("embedded/templates/Dockerfile")
	if err != nil {
		t.Fatalf("embedded Dockerfile missing: %v", err)
	}
	if !regexp.MustCompile(`(?m)^\s+tmux\s+\\$`).Match(df) {
		t.Error("Dockerfile's apt-get install block does not list tmux")
	}
}
```

- [ ] **Step 6: Run the tests to verify they fail**

Run: `cd /Users/elliott/Projects/cspace && make sync-embedded && go test ./internal/assets/ -run 'TestDockerfile' -v`
Expected: FAIL — `no COPY line in lib/templates/Dockerfile ships lib/runtime/tmux.conf` and `Dockerfile's apt-get install block does not list tmux`.

- [ ] **Step 7: Install tmux and COPY the config in the Dockerfile**

In `lib/templates/Dockerfile`, in the comment block above the apt install, add one bullet after the `gh` line:

```
#   - tmux: persistent in-sandbox sessions — `cspace attach` runs inside one so
#     a closed host window does not end (or orphan) the session
```

In the `RUN apt-get update && apt-get install -y --no-install-recommends` list, insert `tmux` between `sudo` and `unzip` (the list is alphabetical):

```dockerfile
    sudo \
    tmux \
    unzip \
```

Then, in the runtime-scripts COPY block (immediately after the `COPY lib/runtime/scripts/statusline.sh …` line and before the `RUN chmod +x` that follows it), add:

```dockerfile
# tmux config for in-sandbox sessions. Passed explicitly with `tmux -f` by
# every cspace attach, so ~/.tmux.conf inside the sandbox is never consulted
# and a stray user binding can never swallow one of Claude's keys.
RUN mkdir -p /usr/local/etc
COPY lib/runtime/tmux.conf /usr/local/etc/cspace-tmux.conf
RUN test -f /usr/local/etc/cspace-tmux.conf
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `cd /Users/elliott/Projects/cspace && make sync-embedded && go test ./internal/assets/ -v`
Expected: PASS for every test in the package, including `TestDockerfileCopiesEveryEmbeddedRuntimeFile` and `TestDockerfileInstallsTmux`.

- [ ] **Step 9: Run the full check**

Run: `cd /Users/elliott/Projects/cspace && make check`
Expected: exits 0.

- [ ] **Step 10: Commit**

```bash
cd /Users/elliott/Projects/cspace
git add lib/runtime/tmux.conf lib/templates/Dockerfile Makefile internal/assets/assets_test.go
git commit -m "Ship tmux and cspace-tmux.conf in the sandbox image"
```

---

### Task 3: Add the agent-state hook script

**Files:**
- Create: `lib/runtime/scripts/cspace-agent-state.sh`
- Create: `lib/runtime/scripts/cspace-agent-state.test.sh`
- Modify: `lib/templates/Dockerfile` (runtime-script COPY block and the `chmod +x` that follows)

**Interfaces:**
- Consumes: nothing.
- Produces: `/usr/local/bin/cspace-agent-state.sh <state>` inside the image, where `<state>` is one of `starting`, `working`, `needs-input`, `idle`, `exited`. Task 4's hooks block invokes exactly that path with exactly those five states. Output file: `/sessions/agent-state.json`, overridable for tests with `CSPACE_AGENT_STATE_FILE`.

- [ ] **Step 1: Write the failing test**

Create `lib/runtime/scripts/cspace-agent-state.test.sh`:

```bash
#!/usr/bin/env bash
# Tests cspace-agent-state.sh: the JSON it writes, the states it refuses, and
# that the write is atomic (never truncates the target in place).
#
# Needs jq, which the sandbox image has and a stock macOS host may not — so
# this skips rather than fails when jq is absent, like statusline.test.sh.
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$HERE/cspace-agent-state.sh"
fail() { echo "FAIL: $1"; exit 1; }

if ! command -v jq >/dev/null 2>&1; then
  echo "SKIP: jq not installed"
  exit 0
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
STATE_FILE="$TMP/agent-state.json"

PAYLOAD='{"session_id":"sess-abc","hook_event_name":"UserPromptSubmit","cwd":"/workspace"}'

# ── a normal hook call records state, session and event ───────────────────
printf '%s' "$PAYLOAD" | CSPACE_AGENT_STATE_FILE="$STATE_FILE" bash "$SCRIPT" working \
  || fail "script exited non-zero on a normal call"
[ -f "$STATE_FILE" ] || fail "no state file written"

jq -e '.state == "working"' "$STATE_FILE" >/dev/null \
  || fail "state not recorded: $(cat "$STATE_FILE")"
jq -e '.session_id == "sess-abc"' "$STATE_FILE" >/dev/null \
  || fail "session_id not taken from the hook payload: $(cat "$STATE_FILE")"
jq -e '.event == "UserPromptSubmit"' "$STATE_FILE" >/dev/null \
  || fail "event not taken from the hook payload: $(cat "$STATE_FILE")"
jq -e '.at | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$")' "$STATE_FILE" >/dev/null \
  || fail "timestamp is not RFC3339 UTC: $(cat "$STATE_FILE")"

# ── every state the hooks table uses is accepted ──────────────────────────
for s in starting working needs-input idle exited; do
  printf '%s' "$PAYLOAD" | CSPACE_AGENT_STATE_FILE="$STATE_FILE" bash "$SCRIPT" "$s" \
    || fail "state '$s' rejected"
  jq -e --arg s "$s" '.state == $s' "$STATE_FILE" >/dev/null || fail "state '$s' not written"
done

# ── an unknown state leaves the last good value and still exits 0 ─────────
# A PreToolUse hook exiting non-zero BLOCKS the tool call, so this script may
# never fail a turn over its own bookkeeping.
printf '%s' "$PAYLOAD" | CSPACE_AGENT_STATE_FILE="$STATE_FILE" bash "$SCRIPT" bogus 2>/dev/null \
  || fail "unknown state exited non-zero — that would block a tool call"
jq -e '.state == "exited"' "$STATE_FILE" >/dev/null \
  || fail "unknown state overwrote the last good value: $(cat "$STATE_FILE")"

# ── no argument at all is also survivable ─────────────────────────────────
printf '%s' "$PAYLOAD" | CSPACE_AGENT_STATE_FILE="$STATE_FILE" bash "$SCRIPT" 2>/dev/null \
  || fail "missing state argument exited non-zero"

# ── the write is atomic: a failed render never truncates the target ───────
# Stub jq with one that fails, so the temp file comes out empty. A script
# writing straight to the target would leave it empty or half-written; the
# host polls this file once a second and must never read a torn one.
mkdir -p "$TMP/bin"
cat > "$TMP/bin/jq" <<'EOF'
#!/usr/bin/env bash
exit 1
EOF
chmod +x "$TMP/bin/jq"
before="$(cat "$STATE_FILE")"
printf '%s' "$PAYLOAD" | PATH="$TMP/bin:$PATH" CSPACE_AGENT_STATE_FILE="$STATE_FILE" \
  bash "$SCRIPT" idle 2>/dev/null || fail "failed render exited non-zero"
[ "$(cat "$STATE_FILE")" = "$before" ] \
  || fail "a failed render clobbered the target: $(cat "$STATE_FILE")"

# ── and it leaves no temp files behind ────────────────────────────────────
leftovers="$(find "$TMP" -maxdepth 1 -name 'agent-state.json.tmp.*' | wc -l | tr -d ' ')"
[ "$leftovers" = "0" ] || fail "left $leftovers temp file(s) behind"

echo "PASS"
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd /Users/elliott/Projects/cspace && bash lib/runtime/scripts/cspace-agent-state.test.sh`
Expected: FAIL — bash reports `cspace-agent-state.sh: No such file or directory`, and the script prints `FAIL: script exited non-zero on a normal call`.

- [ ] **Step 3: Write the script**

Create `lib/runtime/scripts/cspace-agent-state.sh`:

```bash
#!/usr/bin/env bash
# cspace agent-state hook — records what the sandbox's interactive Claude
# session is doing, so the host can render it without asking Claude anything.
#
# Invoked from the hooks block that cspace-entrypoint.sh writes into
# ~/.claude/settings.json:
#
#     cspace-agent-state.sh <state>
#
# with <state> one of: starting working needs-input idle exited.
# Claude Code pipes the hook payload as JSON on stdin; session_id and
# hook_event_name are read from there.
#
# The file is /sessions/agent-state.json, which is the host's
# ~/.cspace/sessions/<project>/<sandbox>/agent-state.json through the existing
# bind mount — so the host reads it directly, with no control-port round trip.
# The write is atomic (temp file in the same directory, then rename) because
# the host polls it about once a second and must never read half a record.
#
# This script NEVER exits non-zero. A PreToolUse hook that exits 2 blocks the
# tool call, and a state file is not worth failing an agent's turn over.
set -u

STATE="${1:-}"
STATE_FILE="${CSPACE_AGENT_STATE_FILE:-/sessions/agent-state.json}"

case "$STATE" in
    starting|working|needs-input|idle|exited) ;;
    *)
        echo "cspace-agent-state: ignoring unknown state '${STATE}'" >&2
        exit 0
        ;;
esac

# Read the hook payload only when stdin is a pipe. Run by hand from a
# terminal, `cat` would block forever waiting for EOF.
payload=""
if [ ! -t 0 ]; then
    payload="$(cat)"
fi

session_id=""
event=""
if [ -n "$payload" ] && command -v jq >/dev/null 2>&1; then
    session_id="$(printf '%s' "$payload" | jq -r '.session_id // ""' 2>/dev/null)"
    event="$(printf '%s' "$payload" | jq -r '.hook_event_name // ""' 2>/dev/null)"
fi
[ -n "$event" ] || event="${2:-}"

now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
dir="$(dirname "$STATE_FILE")"
mkdir -p "$dir" 2>/dev/null || true
tmp="${STATE_FILE}.tmp.$$"

if command -v jq >/dev/null 2>&1; then
    jq -n --arg state "$STATE" --arg at "$now" \
          --arg session_id "$session_id" --arg event "$event" \
          '{state: $state, at: $at, session_id: $session_id, event: $event}' \
          > "$tmp" 2>/dev/null
else
    # No jq (a project image that trimmed it). The fields are ours and
    # contain no quotes, so a printf is safe enough for the fallback.
    printf '{"state":"%s","at":"%s","session_id":"%s","event":"%s"}\n' \
        "$STATE" "$now" "$session_id" "$event" > "$tmp" 2>/dev/null
fi

# Only publish a non-empty render; a failed one must leave the previous
# state in place rather than truncate it.
if [ -s "$tmp" ]; then
    mv -f "$tmp" "$STATE_FILE" 2>/dev/null || rm -f "$tmp"
else
    rm -f "$tmp"
fi

exit 0
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd /Users/elliott/Projects/cspace && chmod +x lib/runtime/scripts/cspace-agent-state.sh && bash lib/runtime/scripts/cspace-agent-state.test.sh`
Expected: `PASS`.

- [ ] **Step 5: Ship the script in the image**

In `lib/templates/Dockerfile`, add one COPY line to the runtime-scripts block, after the `RUN test -f /usr/local/etc/cspace-tmux.conf` line Task 2 added and before the `RUN chmod +x` block:

```dockerfile
COPY lib/runtime/scripts/cspace-agent-state.sh /usr/local/bin/cspace-agent-state.sh
```

and add it to the `chmod +x` that follows, so the block reads:

```dockerfile
RUN chmod +x /usr/local/bin/cspace-entrypoint.sh \
            /usr/local/bin/cspace-supervisor-loop.sh \
            /usr/local/bin/cspace-install-plugins.sh \
            /usr/local/bin/cspace-agent-state.sh \
            /usr/local/bin/cspace-statusline.sh
```

- [ ] **Step 6: Verify the COPY drift guard and shellcheck are green**

Run: `cd /Users/elliott/Projects/cspace && make sync-embedded && go test ./internal/assets/ -run TestDockerfileCopiesEveryEmbeddedRuntimeFile -v && shellcheck lib/runtime/scripts/cspace-agent-state.sh lib/runtime/scripts/cspace-agent-state.test.sh`
Expected: PASS, and shellcheck prints nothing.

- [ ] **Step 7: Run the full check**

Run: `cd /Users/elliott/Projects/cspace && make check`
Expected: exits 0, and the `make test-scripts` section prints `bash lib/runtime/scripts/cspace-agent-state.test.sh` followed by `PASS`.

- [ ] **Step 8: Commit**

```bash
cd /Users/elliott/Projects/cspace
git add lib/runtime/scripts/cspace-agent-state.sh lib/runtime/scripts/cspace-agent-state.test.sh lib/templates/Dockerfile
git commit -m "Add cspace-agent-state.sh and ship it in the image"
```

---

### Task 4: Seed the agent-state hooks in the entrypoint's settings

**Files:**
- Modify: `lib/runtime/scripts/cspace-entrypoint.sh` (the `SETTINGS_JSON` block, lines ~95-112)
- Create: `lib/runtime/scripts/cspace-entrypoint.test.sh`

**Interfaces:**
- Consumes: `/usr/local/bin/cspace-agent-state.sh <state>` from Task 3.
- Produces: `~/.claude/settings.json` with a `hooks` key carrying exactly nine events — `SessionStart`, `UserPromptSubmit`, `PostToolUse`, `PreToolUse`, `PermissionRequest`, `Notification`, `Stop`, `StopFailure`, `SessionEnd` — each with exactly one entry.

- [ ] **Step 1: Write the failing test**

Create `lib/runtime/scripts/cspace-entrypoint.test.sh`:

```bash
#!/usr/bin/env bash
# Tests the settings.json seed in cspace-entrypoint.sh without running the
# entrypoint itself (which wants sudo, iptables, dnsmasq and a network).
#
# The two heredocs are extracted from the script and re-rendered in a fresh
# bash with the variables the entrypoint would have set, then asserted with
# jq. That catches the thing editing a JSON heredoc actually breaks: a stray
# comma, a missing brace, a hook pointed at the wrong path.
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$HERE/cspace-entrypoint.sh"
fail() { echo "FAIL: $1"; exit 1; }

if ! command -v jq >/dev/null 2>&1; then
  echo "SKIP: jq not installed"
  exit 0
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# extract_heredoc <terminator> — the body between `<<TERM` and the closing
# `TERM`, exclusive.
extract_heredoc() {
  sed -n "/<<$1\$/,/^$1\$/p" "$SCRIPT" | sed '1d;$d'
}

# render <terminator> — re-run the heredoc in a fresh shell so ${vars} expand
# exactly as they would at boot. Variables come from the environment.
render() {
  {
    echo "cat <<$1"
    extract_heredoc "$1"
    echo "$1"
  } > "$TMP/render-$1.sh"
  bash "$TMP/render-$1.sh"
}

AGENT_STATE_CMD=/usr/local/bin/cspace-agent-state.sh
export AGENT_STATE_CMD
hooks="$(render HOOKS)"
[ -n "$hooks" ] || fail "no HOOKS heredoc found in the entrypoint"

export statusline_cmd=/usr/local/bin/cspace-statusline.sh
export hooks_block="$hooks"
settings="$(render JSON)"

printf '%s' "$settings" > "$TMP/settings.json"
jq -e . "$TMP/settings.json" >/dev/null \
  || fail "settings.json is not valid JSON with the hooks block: $settings"

# ── the pre-existing gate-suppression keys survive ────────────────────────
jq -e '.statusLine.command == "/usr/local/bin/cspace-statusline.sh"' "$TMP/settings.json" >/dev/null \
  || fail "statusLine lost"
jq -e '.permissions.defaultMode == "bypassPermissions"' "$TMP/settings.json" >/dev/null \
  || fail "permissions.defaultMode lost"
jq -e '.skipDangerousModePermissionPrompt == true' "$TMP/settings.json" >/dev/null \
  || fail "skipDangerousModePermissionPrompt lost"
jq -e '.enableAllProjectMcpServers == true' "$TMP/settings.json" >/dev/null \
  || fail "enableAllProjectMcpServers lost"

# ── every event in the design's table, and only those ─────────────────────
want_events='["Notification","PermissionRequest","PostToolUse","PreToolUse","SessionEnd","SessionStart","Stop","StopFailure","UserPromptSubmit"]'
jq -e --argjson want "$want_events" '(.hooks | keys) == $want' "$TMP/settings.json" >/dev/null \
  || fail "hook events are $(jq -c '.hooks | keys' "$TMP/settings.json"), want $want_events"

# ── no event has two entries, so no two hooks race to write one event ─────
jq -e '[.hooks[] | length] | max == 1' "$TMP/settings.json" >/dev/null \
  || fail "an event has more than one hook entry"

# ── the state each event records ──────────────────────────────────────────
check_state() {  # $1=event  $2=expected state
  jq -e --arg e "$1" --arg s "$2" \
    '.hooks[$e][0].hooks[0].command == "/usr/local/bin/cspace-agent-state.sh " + $s' \
    "$TMP/settings.json" >/dev/null \
    || fail "$1 does not record '$2': $(jq -c --arg e "$1" '.hooks[$e]' "$TMP/settings.json")"
}
check_state SessionStart starting
check_state UserPromptSubmit working
check_state PostToolUse working
check_state PreToolUse needs-input
check_state PermissionRequest needs-input
check_state Notification idle
check_state Stop idle
check_state StopFailure idle
check_state SessionEnd exited

# ── every hook is a command hook ──────────────────────────────────────────
jq -e '[.hooks[][] | .hooks[] | .type] | unique == ["command"]' "$TMP/settings.json" >/dev/null \
  || fail "a hook is not type=command"

# ── matchers: PreToolUse only on AskUserQuestion, Notification on idle ────
# Hooks matching the same event run in parallel, which is why the generic
# "working" comes from PostToolUse and PreToolUse is narrowed to the one
# tool that asks the user something.
jq -e '.hooks.PreToolUse[0].matcher == "AskUserQuestion"' "$TMP/settings.json" >/dev/null \
  || fail "PreToolUse is not narrowed to AskUserQuestion"
jq -e '.hooks.Notification[0].matcher == "idle_prompt"' "$TMP/settings.json" >/dev/null \
  || fail "Notification is not narrowed to idle_prompt"
jq -e '.hooks.PostToolUse[0].matcher == "*"' "$TMP/settings.json" >/dev/null \
  || fail "PostToolUse does not match every tool"

# ── with no state script in the image, the file is still valid JSON ───────
# An older/project image has no cspace-agent-state.sh; hooks pointed at a
# missing binary would fail on every single turn and show a banner.
export hooks_block=""
render JSON > "$TMP/settings-nohooks.json"
jq -e . "$TMP/settings-nohooks.json" >/dev/null \
  || fail "settings.json is invalid JSON with an empty hooks block: $(cat "$TMP/settings-nohooks.json")"
jq -e '.hooks == null' "$TMP/settings-nohooks.json" >/dev/null \
  || fail "empty hooks block still produced a hooks key"
jq -e '.statusLine.command == "/usr/local/bin/cspace-statusline.sh"' "$TMP/settings-nohooks.json" >/dev/null \
  || fail "statusLine lost on the no-hooks path"

echo "PASS"
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd /Users/elliott/Projects/cspace && bash lib/runtime/scripts/cspace-entrypoint.test.sh`
Expected: `FAIL: no HOOKS heredoc found in the entrypoint`.

- [ ] **Step 3: Build the hooks block and interpolate it into the settings heredoc**

In `lib/runtime/scripts/cspace-entrypoint.sh`, immediately **before** the existing `cat > "$SETTINGS_JSON" <<JSON` line (after the `statusline_cmd` lines), insert:

```bash
# Agent-state hooks. Every entry runs cspace-agent-state.sh <state>, which
# writes /sessions/agent-state.json — the host's
# ~/.cspace/sessions/<project>/<sandbox>/agent-state.json — so the control
# plane can show what this sandbox's interactive session is doing without
# asking Claude anything.
#
# One entry per event, never two: hooks matching the same event run in
# parallel, so two entries on one event would race to write different states.
# That is why the generic "working" comes from PostToolUse (a tool finished,
# Claude continues) and PreToolUse is narrowed to AskUserQuestion. Stop does
# not fire on a user interrupt, which is why the host also keeps an
# output-activity heuristic on top of this file.
#
# Emitted only when the state script is actually in the image: a project that
# pins its own image has no /usr/local/bin/cspace-agent-state.sh, and hooks
# pointed at a missing command fail on every turn with a visible banner.
AGENT_STATE_CMD=/usr/local/bin/cspace-agent-state.sh
hooks_block=""
if [ -x "$AGENT_STATE_CMD" ]; then
    hooks_block=$(cat <<HOOKS
  "hooks": {
    "SessionStart": [{ "hooks": [{ "type": "command", "command": "${AGENT_STATE_CMD} starting" }] }],
    "UserPromptSubmit": [{ "hooks": [{ "type": "command", "command": "${AGENT_STATE_CMD} working" }] }],
    "PostToolUse": [{ "matcher": "*", "hooks": [{ "type": "command", "command": "${AGENT_STATE_CMD} working" }] }],
    "PreToolUse": [{ "matcher": "AskUserQuestion", "hooks": [{ "type": "command", "command": "${AGENT_STATE_CMD} needs-input" }] }],
    "PermissionRequest": [{ "hooks": [{ "type": "command", "command": "${AGENT_STATE_CMD} needs-input" }] }],
    "Notification": [{ "matcher": "idle_prompt", "hooks": [{ "type": "command", "command": "${AGENT_STATE_CMD} idle" }] }],
    "Stop": [{ "hooks": [{ "type": "command", "command": "${AGENT_STATE_CMD} idle" }] }],
    "StopFailure": [{ "hooks": [{ "type": "command", "command": "${AGENT_STATE_CMD} idle" }] }],
    "SessionEnd": [{ "hooks": [{ "type": "command", "command": "${AGENT_STATE_CMD} exited" }] }]
  },
HOOKS
)
fi
```

Then change the settings heredoc itself to interpolate it — `${hooks_block}` sits on its own line and carries its own trailing comma, so an empty block renders as a blank line and the JSON stays valid:

```bash
cat > "$SETTINGS_JSON" <<JSON
{
${hooks_block}
  "statusLine": {
    "type": "command",
    "command": "${statusline_cmd}"
  },
  "theme": "dark",
  "permissions": { "defaultMode": "bypassPermissions" },
  "skipDangerousModePermissionPrompt": true,
  "enableAllProjectMcpServers": true
}
JSON
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd /Users/elliott/Projects/cspace && bash lib/runtime/scripts/cspace-entrypoint.test.sh`
Expected: `PASS`.

- [ ] **Step 5: Run shellcheck and the full check**

Run: `cd /Users/elliott/Projects/cspace && shellcheck lib/runtime/scripts/cspace-entrypoint.sh lib/runtime/scripts/cspace-entrypoint.test.sh && make check`
Expected: shellcheck prints nothing; `make check` exits 0.

- [ ] **Step 6: Commit**

```bash
cd /Users/elliott/Projects/cspace
git add lib/runtime/scripts/cspace-entrypoint.sh lib/runtime/scripts/cspace-entrypoint.test.sh
git commit -m "Seed agent-state hooks in the entrypoint settings"
```

---

### Task 5: Move the attach argv and TERM mapping into internal/control

**Files:**
- Create: `internal/control/control.go`
- Create: `internal/control/argv.go`
- Create: `internal/control/argv_test.go`
- Modify: `internal/cli/cmd_attach.go` (delete `terminalEnv`, `sandboxTERM`, `applyTerminalEnv`, `terminalEnvArgs`, `attachArgs`)
- Delete: `internal/cli/cmd_attach_test.go` (every test in it moves to `internal/control/argv_test.go`; Task 8 recreates the file)
- Modify: `internal/cli/cmd_up.go:408` (`applyTerminalEnv` → `control.ApplyTerminalEnv`, plus the `internal/control` import)
- Modify: `internal/cli/tui_actor.go:35` (call through `control`)

**Interfaces:**
- Consumes: `/usr/local/etc/cspace-tmux.conf` from Task 2.
- Produces:
  - `control.SessionClaude = "cspace-claude"`, `control.SessionShell = "cspace-shell"`, `control.TmuxConf = "/usr/local/etc/cspace-tmux.conf"`, `control.Workspace = "/workspace"`
  - `type control.AttachSpec struct { Container, Session string; Command []string; TERM, COLORTERM string }`
  - `func control.ClaudeAttach(container string, tmux bool) AttachSpec`
  - `func control.AttachArgv(spec AttachSpec) (bin string, argv []string, err error)`
  - `func control.TerminalEnv(term, colorterm string) map[string]string`
  - `func control.TerminalEnvArgs(term, colorterm string) []string`
  - `func control.ApplyTerminalEnv(env map[string]string, term, colorterm string)`

  There is deliberately no `cli.attachArgs` adapter left behind: both CLI call sites build the spec with `control.ClaudeAttach` because Task 8 needs `spec.Session` for the detach bookkeeping, and a wrapper that hid it would just have to be unwrapped again.

  **`AttachArgv` takes one struct, not four strings**, because `AttachSpec.Command` is what decides whether the session runs `claude` or a shell. The rollout step 2 plan (`docs/superpowers/plans/2026-09-17-control-plane-2-control-api.md`) assumes exactly these names and this signature and is told not to redefine them, so any change to the shape above has to be mirrored there — note that it files `AttachArgv` under `attach.go` in its closing executor note, while this task puts it in `argv.go`.

- [ ] **Step 1: Write the failing test**

Create `internal/control/argv_test.go`:

```go
package control

import (
	"strings"
	"testing"
)

// TestAttachArgv pins the exec argv both attach paths build. `-A` on
// new-session is the whole persistence story: it attaches when the session
// exists and ignores the command, so the command runs only on creation and a
// second attach shares the first one's screen.
func TestAttachArgv(t *testing.T) {
	cases := []struct {
		name string
		spec AttachSpec
		want []string
	}{
		{
			name: "tmux session, TERM forwarded",
			spec: AttachSpec{
				Container: "cspace-demo-mercury",
				Session:   SessionClaude,
				Command:   []string{"claude", "--dangerously-skip-permissions"},
				TERM:      "xterm-256color",
			},
			want: []string{
				"container", "exec", "-it",
				"-e", "TERM=xterm-256color",
				"cspace-demo-mercury",
				"tmux", "-f", "/usr/local/etc/cspace-tmux.conf",
				"new-session", "-A", "-s", "cspace-claude", "-c", "/workspace",
				"claude", "--dangerously-skip-permissions",
			},
		},
		{
			// The fallback for an image built before cspace shipped tmux:
			// exactly the argv attach used before this change.
			name: "no session falls back to a direct exec",
			spec: AttachSpec{
				Container: "cspace-demo-mercury",
				Command:   []string{"claude", "--dangerously-skip-permissions"},
				TERM:      "xterm-256color",
			},
			want: []string{
				"container", "exec", "-it",
				"-e", "TERM=xterm-256color",
				"cspace-demo-mercury",
				"claude", "--dangerously-skip-permissions",
			},
		},
		{
			// An exotic TERM is substituted (Debian terminfo has no
			// xterm-ghostty) and COLORTERM is forwarded as-is.
			name: "exotic TERM substituted, COLORTERM forwarded",
			spec: AttachSpec{
				Container: "cspace-demo-venus",
				Session:   SessionShell,
				Command:   []string{"bash", "-l"},
				TERM:      "xterm-ghostty",
				COLORTERM: "truecolor",
			},
			want: []string{
				"container", "exec", "-it",
				"-e", "COLORTERM=truecolor",
				"-e", "TERM=xterm-256color",
				"cspace-demo-venus",
				"tmux", "-f", "/usr/local/etc/cspace-tmux.conf",
				"new-session", "-A", "-s", "cspace-shell", "-c", "/workspace",
				"bash", "-l",
			},
		},
		{
			name: "no terminal at all adds no -e flags",
			spec: AttachSpec{
				Container: "cspace-demo-mercury",
				Session:   SessionClaude,
				Command:   []string{"claude", "--dangerously-skip-permissions"},
			},
			want: []string{
				"container", "exec", "-it",
				"cspace-demo-mercury",
				"tmux", "-f", "/usr/local/etc/cspace-tmux.conf",
				"new-session", "-A", "-s", "cspace-claude", "-c", "/workspace",
				"claude", "--dangerously-skip-permissions",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bin, argv, err := AttachArgv(tc.spec)
			if err != nil {
				// container may not be on PATH in CI.
				t.Skipf("container CLI not resolvable: %v", err)
			}
			if !strings.HasSuffix(bin, "container") {
				t.Errorf("bin = %q, want it to resolve the container binary", bin)
			}
			if len(argv) != len(tc.want) {
				t.Fatalf("argv = %v, want %v", argv, tc.want)
			}
			for i := range tc.want {
				if argv[i] != tc.want[i] {
					t.Errorf("argv[%d] = %q, want %q", i, argv[i], tc.want[i])
				}
			}
		})
	}
}

func TestAttachArgvRejectsEmptyInput(t *testing.T) {
	if _, _, err := AttachArgv(AttachSpec{Command: []string{"claude"}}); err == nil {
		t.Error("empty container name was accepted")
	}
	if _, _, err := AttachArgv(AttachSpec{Container: "c"}); err == nil {
		t.Error("empty command was accepted")
	}
}

// TestClaudeAttach — the interactive path forwards the terminal that is
// actually attaching, and only asks for tmux when the sandbox has it.
func TestClaudeAttach(t *testing.T) {
	t.Setenv("TERM", "xterm-ghostty")
	t.Setenv("COLORTERM", "truecolor")

	withTmux := ClaudeAttach("cspace-demo-mercury", true)
	if withTmux.Session != SessionClaude {
		t.Errorf("Session = %q, want %q", withTmux.Session, SessionClaude)
	}
	if withTmux.TERM != "xterm-ghostty" || withTmux.COLORTERM != "truecolor" {
		t.Errorf("terminal not read from the environment: %+v", withTmux)
	}
	wantCmd := []string{"claude", "--dangerously-skip-permissions"}
	if len(withTmux.Command) != len(wantCmd) {
		t.Fatalf("Command = %v, want %v", withTmux.Command, wantCmd)
	}
	for i := range wantCmd {
		if withTmux.Command[i] != wantCmd[i] {
			t.Errorf("Command[%d] = %q, want %q", i, withTmux.Command[i], wantCmd[i])
		}
	}

	without := ClaudeAttach("cspace-demo-mercury", false)
	if without.Session != "" {
		t.Errorf("Session = %q, want empty when the image has no tmux", without.Session)
	}
}

// TestTerminalEnv covers the color-support signal cspace hands a sandbox.
// Apple Container injects a bare TERM=xterm when it allocates a TTY and never
// sets COLORTERM, which Node's color detection reads as 16 colors — so Claude
// inside a sandbox paints with 16 while the same terminal gives it 16.7M
// outside. These values are what close that gap.
func TestTerminalEnv(t *testing.T) {
	cases := []struct {
		name      string
		term      string
		colorterm string
		want      map[string]string
	}{
		{
			// The sandbox's terminfo database is Debian's; xterm-ghostty is
			// not in it, and ncurses tools (vim, less) fail outright on an
			// unknown terminal type. Substitute an entry it does have.
			name: "exotic TERM is substituted, COLORTERM forwarded",
			term: "xterm-ghostty", colorterm: "truecolor",
			want: map[string]string{"TERM": "xterm-256color", "COLORTERM": "truecolor"},
		},
		{
			name: "a terminfo entry the sandbox has passes through",
			term: "screen-256color", colorterm: "truecolor",
			want: map[string]string{"TERM": "screen-256color", "COLORTERM": "truecolor"},
		},
		{
			// Claiming truecolor the host never claimed would be inventing
			// capability; 256 colors is still a 16x improvement on xterm.
			name: "no COLORTERM on the host means none in the sandbox",
			term: "xterm-256color", colorterm: "",
			want: map[string]string{"TERM": "xterm-256color"},
		},
		{
			// TERM=dumb means "emit no escape codes at all" — dressing that
			// up would put escape sequences into whatever is capturing output.
			name: "dumb terminal is left alone",
			term: "dumb", colorterm: "truecolor",
			want: map[string]string{},
		},
		{
			name: "no TERM at all (cron, pipe) is left alone",
			term: "", colorterm: "",
			want: map[string]string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TerminalEnv(tc.term, tc.colorterm)
			if len(got) != len(tc.want) {
				t.Fatalf("TerminalEnv(%q, %q) = %v, want %v", tc.term, tc.colorterm, got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

// TestTerminalEnvArgs pins the flag form and its ordering, since argv is what
// attach actually execs.
func TestTerminalEnvArgs(t *testing.T) {
	got := TerminalEnvArgs("xterm-ghostty", "truecolor")
	want := []string{"-e", "COLORTERM=truecolor", "-e", "TERM=xterm-256color"}
	if len(got) != len(want) {
		t.Fatalf("TerminalEnvArgs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if len(TerminalEnvArgs("dumb", "")) != 0 {
		t.Error("dumb terminal produced -e flags")
	}
}

// TestApplyTerminalEnvNeverOverridesExisting — the baked values are a default
// for a container that would otherwise be told TERM=xterm. Anything the
// project or the user set explicitly (devcontainer containerEnv, --env) is a
// deliberate choice and outranks it.
func TestApplyTerminalEnvNeverOverridesExisting(t *testing.T) {
	env := map[string]string{"TERM": "screen"}
	ApplyTerminalEnv(env, "xterm-ghostty", "truecolor")

	if env["TERM"] != "screen" {
		t.Errorf("TERM = %q, want the pre-existing \"screen\" to survive", env["TERM"])
	}
	if env["COLORTERM"] != "truecolor" {
		t.Errorf("COLORTERM = %q, want it seeded alongside", env["COLORTERM"])
	}
}

// TestApplyTerminalEnvSeedsAnEmptyMap is the ordinary boot: nothing set, so
// both land and the sandbox stops reporting 16 colors.
func TestApplyTerminalEnvSeedsAnEmptyMap(t *testing.T) {
	env := map[string]string{}
	ApplyTerminalEnv(env, "xterm-ghostty", "truecolor")

	if env["TERM"] != "xterm-256color" || env["COLORTERM"] != "truecolor" {
		t.Errorf("env = %v, want TERM=xterm-256color COLORTERM=truecolor", env)
	}
}

// TestApplyTerminalEnvHeadlessBootAddsNothing — `cspace up` from a cron job or
// a pipe has no terminal to describe, and inventing one would put escape codes
// into captured output.
func TestApplyTerminalEnvHeadlessBootAddsNothing(t *testing.T) {
	env := map[string]string{}
	ApplyTerminalEnv(env, "", "")

	if len(env) != 0 {
		t.Errorf("env = %v, want it untouched", env)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd /Users/elliott/Projects/cspace && go test ./internal/control/ -v`
Expected: FAIL — `no required module provides package` / `build failed`, because `internal/control` does not exist yet.

- [ ] **Step 3: Write the package**

Create `internal/control/control.go`:

```go
// Package control holds cspace's queries and actions as plain Go functions:
// no terminal code, no cobra, no bubbletea. It is the single implementation
// the CLI commands and (from rollout step 2 of the control-plane design) the
// TUI both call, so "how do you attach to a sandbox" has exactly one answer.
//
// This is step 1's slice of it: the attach argv, the tmux plumbing, and the
// per-sandbox client bookkeeping. The Snapshot / AgentStatus / Ports / Events
// queries and the Down / Send / Interrupt / RestartBrowser / Up actions move
// here in later steps.
package control

import "path/filepath"

const (
	// SessionClaude and SessionShell are the tmux sessions cspace keeps
	// inside a sandbox: one per pane kind, so two panes on one sandbox never
	// contend for a current window. `cspace attach` from any terminal joins
	// SessionClaude and shares its screen.
	SessionClaude = "cspace-claude"
	SessionShell  = "cspace-shell"

	// TmuxConf is where the sandbox image puts cspace's tmux config. It is
	// always passed with `tmux -f`, so ~/.tmux.conf and /etc/tmux.conf inside
	// the sandbox are never read and no user binding can swallow a key the
	// host meant for Claude.
	TmuxConf = "/usr/local/etc/cspace-tmux.conf"

	// Workspace is the sandbox's project directory, and the working
	// directory every session starts in.
	Workspace = "/workspace"
)

// ControlPlaneDir is where cspace keeps its per-sandbox attach bookkeeping on
// the host: the attach lock and one record file per live tmux client. It is
// deliberately not the session directory — `cspace down` wipes that, and a
// client record must outlive nothing but its own client.
func ControlPlaneDir(home, project, sandbox string) string {
	return filepath.Join(home, ".cspace", "controlplane", project, sandbox)
}
```

Create `internal/control/argv.go`:

```go
package control

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// AttachSpec describes one interactive attach into a sandbox.
//
// Session empty means "no tmux": Command is exec'd directly, which is the
// fallback for a sandbox whose image predates tmux. That path cannot survive
// the host terminal closing and leaves the command running inside the
// sandbox when it does — callers warn.
type AttachSpec struct {
	Container string   // full container name, e.g. cspace-demo-mercury
	Session   string   // tmux session name; empty runs Command directly
	Command   []string // what the session runs when it is created
	TERM      string   // the host terminal's TERM
	COLORTERM string   // the host terminal's COLORTERM
}

// ClaudeAttach returns the spec for an interactive Claude session, reading
// the host terminal description from the process environment.
//
// --dangerously-skip-permissions matches the v0 default: sandboxes are
// isolated, so the per-tool confirmation prompts that protect host-shell
// users just get in the way. The supervisor's non-interactive runner already
// passes bypassPermissions; this keeps the interactive path consistent.
func ClaudeAttach(container string, tmux bool) AttachSpec {
	spec := AttachSpec{
		Container: container,
		Command:   []string{"claude", "--dangerously-skip-permissions"},
		TERM:      os.Getenv("TERM"),
		COLORTERM: os.Getenv("COLORTERM"),
	}
	if tmux {
		spec.Session = SessionClaude
	}
	return spec
}

// AttachArgv resolves the container binary and builds the exec argv. argv[0]
// is the literal "container" per exec convention, so callers running it as a
// child pass argv[1:].
//
// With a session it is attach-or-create in one argv: `-A` attaches when the
// session already exists and ignores the command, so the command runs only on
// creation and a second attach shares the first one's screen. When the
// command exits the session ends and every client is dropped.
func AttachArgv(spec AttachSpec) (bin string, argv []string, err error) {
	if spec.Container == "" {
		return "", nil, errors.New("attach: empty container name")
	}
	if len(spec.Command) == 0 {
		return "", nil, errors.New("attach: empty command")
	}
	bin, err = exec.LookPath("container")
	if err != nil {
		return "", nil, fmt.Errorf("apple `container` CLI not on PATH: %w", err)
	}
	argv = []string{"container", "exec", "-it"}
	argv = append(argv, TerminalEnvArgs(spec.TERM, spec.COLORTERM)...)
	argv = append(argv, spec.Container)
	if spec.Session != "" {
		argv = append(argv,
			"tmux", "-f", TmuxConf,
			"new-session", "-A", "-s", spec.Session, "-c", Workspace)
	}
	argv = append(argv, spec.Command...)
	return bin, argv, nil
}

// TerminalEnv reports the TERM/COLORTERM a sandbox should see, given the host
// terminal's own values.
//
// Programs pick a palette by reading these two variables — there is no way to
// ask a terminal what it supports. Apple Container injects a bare TERM=xterm
// when it allocates a TTY and never sets COLORTERM, which Node's color
// detection reads as 16 colors. Measured in a real sandbox: TERM=xterm alone
// yields 16, xterm-256color yields 256, and adding COLORTERM=truecolor yields
// 16.7M. That is why Claude's palette flattens inside a sandbox while the same
// terminal renders it fully outside — nothing in the PTY strips color, the
// program simply chooses fewer colors.
//
// TERM is not forwarded verbatim. The sandbox's terminfo database is Debian's,
// and an entry it lacks (xterm-ghostty, say) breaks every ncurses program in
// there with "unknown terminal type" — Claude survives it, `less` and `vim` do
// not. Anything unrecognized is mapped to xterm-256color, which Debian ships.
// COLORTERM is forwarded as-is: claiming truecolor the host never claimed
// would be inventing capability.
func TerminalEnv(term, colorterm string) map[string]string {
	// "dumb" and unset both mean "not an interactive terminal" — output is
	// being captured, and escape codes would be noise in whatever captures it.
	if term == "" || term == "dumb" {
		return map[string]string{}
	}
	out := map[string]string{"TERM": sandboxTERM(term)}
	if colorterm != "" {
		out["COLORTERM"] = colorterm
	}
	return out
}

// sandboxTERM maps a host TERM onto one the sandbox's terminfo database
// actually carries.
func sandboxTERM(term string) string {
	if strings.HasSuffix(term, "-256color") {
		return term
	}
	return "xterm-256color"
}

// ApplyTerminalEnv seeds the container's env with the terminal description,
// leaving any value already there alone. Baking it at create time covers the
// paths attach's per-exec flags don't: a hand-rolled `container exec`, and the
// container's own main process. Apple Container only injects its TERM=xterm
// default when nothing is set, so a baked value survives a TTY exec.
func ApplyTerminalEnv(env map[string]string, term, colorterm string) {
	for k, v := range TerminalEnv(term, colorterm) {
		if _, exists := env[k]; !exists {
			env[k] = v
		}
	}
}

// TerminalEnvArgs renders TerminalEnv as `container exec` flags, ordered by
// key so argv is deterministic.
func TerminalEnvArgs(term, colorterm string) []string {
	env := TerminalEnv(term, colorterm)
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	args := make([]string, 0, len(keys)*2)
	for _, k := range keys {
		args = append(args, "-e", k+"="+env[k])
	}
	return args
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd /Users/elliott/Projects/cspace && go test ./internal/control/ -v`
Expected: PASS for `TestAttachArgv`, `TestAttachArgvRejectsEmptyInput`, `TestClaudeAttach`, `TestTerminalEnv`, `TestTerminalEnvArgs` and the three `TestApplyTerminalEnv*` tests.

- [ ] **Step 5: Point the CLI at the moved code**

In `internal/cli/cmd_attach.go`, delete `terminalEnv`, `sandboxTERM`, `applyTerminalEnv`, `terminalEnvArgs` **and** `attachArgs` outright (the `os/exec`, `sort` and `strings` imports go with them; add `github.com/elliottregan/cspace/internal/control`), and point the one call inside `attachInteractive` at the control API — Task 8 replaces that whole function, this only keeps the tree compiling and the behavior identical apart from the tmux argv:

```go
	bin, argv, err := control.AttachArgv(control.ClaudeAttach(containerName, true))
```

In `internal/cli/cmd_up.go`, add `"github.com/elliottregan/cspace/internal/control"` to the import block — it sorts immediately after the `"github.com/elliottregan/cspace/internal/config"` line the file already has — and change the call on line 408 to the moved function:

```go
			control.ApplyTerminalEnv(env, os.Getenv("TERM"), os.Getenv("COLORTERM"))
```

Without that import the tree does not build: `internal/cli/cmd_up.go:408:4: undefined: control`, and Step 6's `make test` cannot pass.

In `internal/cli/tui_actor.go:35`, the same substitution (Task 8 replaces this call site too), adding the `control` import:

```go
	bin, argv, err := control.AttachArgv(control.ClaudeAttach(row.Container, true))
```

Delete `internal/cli/cmd_attach_test.go`. Every test it held now lives in `internal/control/argv_test.go`: `TestAttachArgv`'s golden table covers both the tmux and the no-tmux argv including the TERM flags, and the five terminal-env tests moved across verbatim. Task 8 recreates the file for the child runner.

```bash
cd /Users/elliott/Projects/cspace && git rm internal/cli/cmd_attach_test.go
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd /Users/elliott/Projects/cspace && make test`
Expected: PASS across `./...`, including `internal/cli` and `internal/control`.

- [ ] **Step 7: Verify no new dependency crept into go.mod**

Run: `cd /Users/elliott/Projects/cspace && git diff --stat go.mod go.sum`
Expected: no output (both unchanged).

- [ ] **Step 8: Run the full check**

Run: `cd /Users/elliott/Projects/cspace && make check`
Expected: exits 0.

- [ ] **Step 9: Commit**

```bash
cd /Users/elliott/Projects/cspace
git add internal/control/control.go internal/control/argv.go internal/control/argv_test.go \
        internal/cli/cmd_attach.go internal/cli/cmd_up.go internal/cli/tui_actor.go
git add -u internal/cli/cmd_attach_test.go
git commit -m "Move the attach argv and TERM mapping into internal/control"
```

---

### Task 6: Probe for tmux and drive its clients

**Files:**
- Create: `internal/control/tmux.go`
- Create: `internal/control/tmux_test.go`

**Interfaces:**
- Consumes: `control.TmuxConf` from Task 5.
- Produces:
  - `type control.Execer interface { Exec(ctx context.Context, container string, cmdline []string) (stdout string, exitCode int, err error) }`
  - `type control.CLIExecer struct{}` implementing it
  - `type control.Tmux struct { Exec Execer; PollEvery, PollFor time.Duration; … }`
  - `func control.NewTmux() *Tmux`
  - `func (*Tmux) Present(ctx context.Context, container string) bool`
  - `func (*Tmux) ListClients(ctx context.Context, container, session string) ([]string, error)`
  - `func (*Tmux) DetachClient(ctx context.Context, container, tty string) error`
  - the `fakeExec` test helper, reused by Task 7's `attach_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/control/tmux_test.go`:

```go
package control

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeExec is the substrate stand-in for every test in this package: it
// records each command it was asked to run and replies from a script keyed by
// call index. Shared with attach_test.go.
type fakeExec struct {
	mu    sync.Mutex
	calls [][]string
	reply func(n int, cmdline []string) (string, int, error)
}

func (f *fakeExec) Exec(_ context.Context, _ string, cmdline []string) (string, int, error) {
	f.mu.Lock()
	n := len(f.calls)
	f.calls = append(f.calls, append([]string(nil), cmdline...))
	reply := f.reply
	f.mu.Unlock()
	if reply == nil {
		return "", 0, nil
	}
	return reply(n, cmdline)
}

func (f *fakeExec) recorded() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]string, len(f.calls))
	copy(out, f.calls)
	return out
}

// testTmux builds a Tmux whose polling is fast enough for a unit test.
func testTmux(f *fakeExec) *Tmux {
	tm := NewTmux()
	tm.Exec = f
	tm.PollEvery = time.Millisecond
	tm.PollFor = 2 * time.Second
	return tm
}

// TestPresentProbesOnceAndMemoizes — an image cannot grow tmux while its
// container runs, so the probe is worth exactly one exec per sandbox. Every
// attach would otherwise pay for it.
func TestPresentProbesOnceAndMemoizes(t *testing.T) {
	f := &fakeExec{reply: func(int, []string) (string, int, error) {
		return "/usr/bin/tmux\n", 0, nil
	}}
	tm := testTmux(f)

	if !tm.Present(context.Background(), "cspace-demo-mercury") {
		t.Fatal("Present() = false for a container whose probe exits 0")
	}
	if !tm.Present(context.Background(), "cspace-demo-mercury") {
		t.Fatal("memoized Present() = false")
	}
	calls := f.recorded()
	if len(calls) != 1 {
		t.Fatalf("probed %d times, want 1: %v", len(calls), calls)
	}
	want := []string{"sh", "-c", "command -v tmux"}
	if strings.Join(calls[0], " ") != strings.Join(want, " ") {
		t.Errorf("probe = %v, want %v", calls[0], want)
	}
}

// TestPresentFalseOnMissingBinary — an image built before cspace shipped
// tmux. The caller falls back to the direct exec and warns.
func TestPresentFalseOnMissingBinary(t *testing.T) {
	f := &fakeExec{reply: func(int, []string) (string, int, error) {
		return "", 1, nil // `command -v tmux` found nothing
	}}
	if testTmux(f).Present(context.Background(), "cspace-demo-mercury") {
		t.Error("Present() = true when the probe exits non-zero")
	}
}

// TestPresentMemoizesPerContainer — two sandboxes can run different images.
func TestPresentMemoizesPerContainer(t *testing.T) {
	f := &fakeExec{reply: func(n int, _ []string) (string, int, error) {
		if n == 0 {
			return "/usr/bin/tmux\n", 0, nil
		}
		return "", 1, nil
	}}
	tm := testTmux(f)
	if !tm.Present(context.Background(), "cspace-demo-mercury") {
		t.Error("first container: Present() = false")
	}
	if tm.Present(context.Background(), "cspace-demo-venus") {
		t.Error("second container: Present() = true, want its own probe to decide")
	}
}

func TestListClients(t *testing.T) {
	f := &fakeExec{reply: func(int, []string) (string, int, error) {
		return "/dev/pts/1\n/dev/pts/2\n\n", 0, nil
	}}
	got, err := testTmux(f).ListClients(context.Background(), "cspace-demo-mercury", SessionClaude)
	if err != nil {
		t.Fatalf("ListClients() error: %v", err)
	}
	want := []string{"/dev/pts/1", "/dev/pts/2"}
	if len(got) != len(want) {
		t.Fatalf("clients = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("client[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	wantCmd := []string{"tmux", "list-clients", "-t", SessionClaude, "-F", "#{client_tty}"}
	if strings.Join(f.recorded()[0], " ") != strings.Join(wantCmd, " ") {
		t.Errorf("command = %v, want %v", f.recorded()[0], wantCmd)
	}
}

// TestListClientsOnNoServer — before the first attach there is no tmux server
// and no session, and tmux exits non-zero saying so. "No clients" is the
// honest answer, not an error: the snapshot before the first attach must
// succeed or nothing can ever be tracked.
func TestListClientsOnNoServer(t *testing.T) {
	f := &fakeExec{reply: func(int, []string) (string, int, error) {
		return "", 1, nil
	}}
	got, err := testTmux(f).ListClients(context.Background(), "cspace-demo-mercury", SessionClaude)
	if err != nil {
		t.Fatalf("ListClients() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("clients = %v, want none", got)
	}
}

// TestDetachClient — this is the call that actually reaps a guest-side
// client. Killing the host-side `container exec` never reaches tmux.
func TestDetachClient(t *testing.T) {
	f := &fakeExec{}
	if err := testTmux(f).DetachClient(context.Background(), "cspace-demo-mercury", "/dev/pts/2"); err != nil {
		t.Fatalf("DetachClient() error: %v", err)
	}
	want := []string{"tmux", "detach-client", "-t", "/dev/pts/2"}
	if strings.Join(f.recorded()[0], " ") != strings.Join(want, " ") {
		t.Errorf("command = %v, want %v", f.recorded()[0], want)
	}
}

func TestDetachClientReportsFailure(t *testing.T) {
	f := &fakeExec{reply: func(int, []string) (string, int, error) {
		return "", 1, nil
	}}
	if err := testTmux(f).DetachClient(context.Background(), "cspace-demo-mercury", "/dev/pts/9"); err == nil {
		t.Error("DetachClient() returned nil for a non-zero tmux exit")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd /Users/elliott/Projects/cspace && go test ./internal/control/ -run 'TestPresent|TestListClients|TestDetachClient' -v`
Expected: FAIL to build — `undefined: NewTmux`, `undefined: Tmux`.

- [ ] **Step 3: Write the implementation**

Create `internal/control/tmux.go`:

```go
package control

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Execer runs a one-shot command inside a sandbox and reports what it printed
// and how it exited. It is the only thing the tmux plumbing needs from the
// substrate, and it is an interface so the plumbing can be tested without
// Apple Container.
//
// A non-zero exit status is NOT an error: tmux uses it to say ordinary things
// like "no server running". Only a transport failure (the CLI missing, the
// context cancelled) returns err.
type Execer interface {
	Exec(ctx context.Context, container string, cmdline []string) (stdout string, exitCode int, err error)
}

// CLIExecer shells out to `container exec <container> <cmdline…>`. No -i, no
// -t: these are bookkeeping calls that must not touch the user's terminal,
// and they run concurrently with an interactive attach that owns it.
type CLIExecer struct{}

// Exec implements Execer.
func (CLIExecer) Exec(ctx context.Context, container string, cmdline []string) (string, int, error) {
	args := append([]string{"exec", container}, cmdline...)
	cmd := exec.CommandContext(ctx, "container", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.String(), exitErr.ExitCode(), nil
	}
	if err != nil {
		return stdout.String(), -1, fmt.Errorf("container exec %s: %w: %s",
			container, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), 0, nil
}

// Tmux drives the tmux server inside a sandbox from the host.
//
// Its job is small and specific: say whether tmux is there at all, list a
// session's clients, and detach one. Everything else about the session
// (creating it, attaching to it) rides the interactive argv from AttachArgv.
type Tmux struct {
	Exec Execer

	// PollEvery and PollFor bound the wait for a new client to appear after
	// an attach starts. Fields rather than constants so tests do not sleep.
	PollEvery time.Duration
	PollFor   time.Duration

	mu      sync.Mutex
	present map[string]bool
}

// NewTmux returns a Tmux wired to the real `container` CLI.
func NewTmux() *Tmux {
	return &Tmux{
		Exec:      CLIExecer{},
		PollEvery: 100 * time.Millisecond,
		PollFor:   10 * time.Second,
		present:   map[string]bool{},
	}
}

// Present reports whether the sandbox's image carries tmux.
//
// One `sh -c 'command -v tmux'` exec, memoized per container for the life of
// the process: an image cannot grow tmux while its container runs, and every
// attach and every pane would otherwise pay for the probe. A sandbox built
// from an image that predates this feature answers false, and callers fall
// back to a direct exec with a warning.
func (t *Tmux) Present(ctx context.Context, container string) bool {
	t.mu.Lock()
	if cached, ok := t.present[container]; ok {
		t.mu.Unlock()
		return cached
	}
	t.mu.Unlock()

	_, code, err := t.Exec.Exec(ctx, container, []string{"sh", "-c", "command -v tmux"})
	ok := err == nil && code == 0

	t.mu.Lock()
	t.present[container] = ok
	t.mu.Unlock()
	return ok
}

// ListClients returns the ttys currently attached to one tmux session.
//
// A session that does not exist yet — or a server that is not running,
// because nothing has attached since the sandbox booted — is not an error.
// The first attach creates both, and the snapshot taken just before it has to
// succeed or there is nothing to diff against.
func (t *Tmux) ListClients(ctx context.Context, container, session string) ([]string, error) {
	out, code, err := t.Exec.Exec(ctx, container,
		[]string{"tmux", "list-clients", "-t", session, "-F", "#{client_tty}"})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, nil
	}
	var ttys []string
	for _, line := range strings.Split(out, "\n") {
		if tty := strings.TrimSpace(line); tty != "" {
			ttys = append(ttys, tty)
		}
	}
	return ttys, nil
}

// DetachClient ends one client's attachment to its session.
//
// This is the call that makes a closed window actually stop being attached:
// killing the host-side `container exec` never reaches the guest, and the
// tmux client it left behind stays attached indefinitely — measured still
// attached until an explicit detach-client was run from a fresh exec.
func (t *Tmux) DetachClient(ctx context.Context, container, tty string) error {
	_, code, err := t.Exec.Exec(ctx, container, []string{"tmux", "detach-client", "-t", tty})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("tmux detach-client -t %s in %s: exit %d", tty, container, code)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd /Users/elliott/Projects/cspace && go test ./internal/control/ -race -v`
Expected: PASS for every test in the package.

- [ ] **Step 5: Run the full check**

Run: `cd /Users/elliott/Projects/cspace && make check`
Expected: exits 0.

- [ ] **Step 6: Commit**

```bash
cd /Users/elliott/Projects/cspace
git add internal/control/tmux.go internal/control/tmux_test.go
git commit -m "Add the tmux probe and client list/detach to internal/control"
```

---

### Task 7: Track the attach's own tmux client under a lock

**Files:**
- Create: `internal/control/attach.go`
- Create: `internal/control/attach_test.go`

**Interfaces:**
- Consumes: `control.Tmux` (Task 6), `control.ControlPlaneDir` (Task 5), the `fakeExec` helper (Task 6).
- Produces:
  - `type control.ClientRecord struct { Session, TTY string; PID int; At string }` (JSON keys `session`, `tty`, `pid`, `at`)
  - `func control.BeginAttach(ctx context.Context, tm *Tmux, container, dir, session string) (*Attachment, error)`
  - `func (*Attachment) Close(ctx context.Context) error`
  - `func (*Attachment) TTY() string`

- [ ] **Step 1: Write the failing test**

Create `internal/control/attach_test.go`:

```go
package control

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// clientScript replies to ListClients calls with successive snapshots and to
// everything else with a clean exit, so a test can say "this is what tmux
// showed before the attach, and this is what it showed after".
func clientScript(snapshots ...string) func(int, []string) (string, int, error) {
	var listN int
	return func(_ int, cmdline []string) (string, int, error) {
		if len(cmdline) > 1 && cmdline[1] == "list-clients" {
			i := listN
			if i >= len(snapshots) {
				i = len(snapshots) - 1
			}
			listN++
			return snapshots[i], 0, nil
		}
		return "", 0, nil
	}
}

// TestBeginAttachRecordsTheNewClient — tmux never tells a client its own tty,
// and the host-side `container exec` cannot ask. The one tty that appears
// between the snapshot before the attach and the one after is this client's,
// which is the whole reason the lock is held across that window.
func TestBeginAttachRecordsTheNewClient(t *testing.T) {
	dir := t.TempDir()
	// Snapshot before the attach (somebody else was already attached), then
	// the one after (ours showed up).
	f := &fakeExec{reply: clientScript(
		"/dev/pts/1\n",
		"/dev/pts/1\n/dev/pts/2\n",
	)}
	tm := testTmux(f)

	att, err := BeginAttach(context.Background(), tm, "cspace-demo-mercury", dir, SessionClaude)
	if err != nil {
		t.Fatalf("BeginAttach() error: %v", err)
	}
	waitTracked(t, att)

	if att.TTY() != "/dev/pts/2" {
		t.Fatalf("TTY() = %q, want the one new tty /dev/pts/2", att.TTY())
	}

	path := filepath.Join(dir, "cspace-claude.dev-pts-2.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no client record at %s: %v", path, err)
	}
	var rec ClientRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("client record is not JSON: %v (%s)", err, data)
	}
	if rec.Session != SessionClaude || rec.TTY != "/dev/pts/2" {
		t.Errorf("record = %+v, want session %q tty /dev/pts/2", rec, SessionClaude)
	}
	if rec.PID != os.Getpid() {
		t.Errorf("record PID = %d, want this process %d — the startup sweep uses it to spot a crashed attach", rec.PID, os.Getpid())
	}
	if _, err := time.Parse(time.RFC3339, rec.At); err != nil {
		t.Errorf("record At = %q, want RFC3339", rec.At)
	}

	// The lock must be free again once the client is known: a second attach
	// to the same sandbox has to be able to start.
	assertLockFree(t, dir)

	// ── Close detaches exactly that client and removes its record ────────
	if err := att.Close(context.Background()); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	var detached bool
	for _, call := range f.recorded() {
		if len(call) >= 4 && call[1] == "detach-client" && call[3] == "/dev/pts/2" {
			detached = true
		}
	}
	if !detached {
		t.Errorf("Close() did not run `tmux detach-client -t /dev/pts/2`: %v", f.recorded())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("client record survived Close(): %v", err)
	}
}

// TestCloseIsIdempotent — attach calls it on the child's exit, and may call it
// again from a signal path.
func TestCloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	f := &fakeExec{reply: clientScript("", "/dev/pts/3\n")}
	att, err := BeginAttach(context.Background(), testTmux(f), "cspace-demo-mercury", dir, SessionClaude)
	if err != nil {
		t.Fatalf("BeginAttach() error: %v", err)
	}
	waitTracked(t, att)

	if err := att.Close(context.Background()); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}
	if err := att.Close(context.Background()); err != nil {
		t.Fatalf("second Close() error: %v", err)
	}
	var detaches int
	for _, call := range f.recorded() {
		if len(call) >= 2 && call[1] == "detach-client" {
			detaches++
		}
	}
	if detaches != 1 {
		t.Errorf("detach ran %d times, want exactly 1", detaches)
	}
}

// TestBeginAttachWithoutSessionIsInert — the no-tmux fallback. Callers must
// not have to nil-check, and nothing may be written for a sandbox that has no
// tmux to detach from.
func TestBeginAttachWithoutSessionIsInert(t *testing.T) {
	dir := t.TempDir()
	f := &fakeExec{}
	att, err := BeginAttach(context.Background(), testTmux(f), "cspace-demo-mercury", dir, "")
	if err != nil {
		t.Fatalf("BeginAttach() error: %v", err)
	}
	if err := att.Close(context.Background()); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if calls := f.recorded(); len(calls) != 0 {
		t.Errorf("inert attachment ran %v", calls)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("inert attachment wrote %d file(s) into the control-plane dir", len(entries))
	}
}

// TestCloseWithoutAKnownClientStillSucceeds — the attach died before its
// client ever showed up (a failed exec). There is nothing to detach, and
// failing here would mask the real error from the child.
func TestCloseWithoutAKnownClientStillSucceeds(t *testing.T) {
	dir := t.TempDir()
	f := &fakeExec{reply: clientScript("")} // no client ever appears
	tm := testTmux(f)
	tm.PollFor = time.Minute // Close must not wait this out

	att, err := BeginAttach(context.Background(), tm, "cspace-demo-mercury", dir, SessionClaude)
	if err != nil {
		t.Fatalf("BeginAttach() error: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- att.Close(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close() error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close() blocked waiting for a client that never appeared")
	}
	assertLockFree(t, dir)
}

// TestBeginAttachFailsWhenTheLockIsHeld — the lock is what makes "the one new
// tty" unambiguous. If another attach is mid-window, this one must not guess.
func TestBeginAttachFailsWhenTheLockIsHeld(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "attach.lock")
	held, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("could not take the lock in the test: %v", err)
	}

	old := attachLockTimeout
	attachLockTimeout = 150 * time.Millisecond
	defer func() { attachLockTimeout = old }()

	f := &fakeExec{reply: clientScript("")}
	if _, err := BeginAttach(context.Background(), testTmux(f), "cspace-demo-mercury", dir, SessionClaude); err == nil {
		t.Error("BeginAttach() succeeded while the attach lock was held")
	}
}

func TestRecordName(t *testing.T) {
	got := recordName(SessionClaude, "/dev/pts/12")
	want := "cspace-claude.dev-pts-12.json"
	if got != want {
		t.Errorf("recordName = %q, want %q", got, want)
	}
}

// waitTracked blocks until the background tracking goroutine has finished
// looking for this attach's client.
func waitTracked(t *testing.T, att *Attachment) {
	t.Helper()
	select {
	case <-att.trackDone:
	case <-time.After(10 * time.Second):
		t.Fatal("client tracking did not finish")
	}
}

// assertLockFree proves the attach lock was released by taking it.
func assertLockFree(t *testing.T, dir string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, "attach.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("attach lock still held: %v", err)
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd /Users/elliott/Projects/cspace && go test ./internal/control/ -run 'TestBeginAttach|TestClose|TestRecordName' -v`
Expected: FAIL to build — `undefined: BeginAttach`, `undefined: ClientRecord`, `undefined: attachLockTimeout`, `undefined: recordName`.

- [ ] **Step 3: Write the implementation**

Create `internal/control/attach.go`:

```go
package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// attachLockTimeout bounds the wait for another process's attach window. A
// var so tests do not sit through it.
var attachLockTimeout = 5 * time.Second

// ClientRecord is the on-disk record of one live tmux client, written to
// <ControlPlaneDir>/<session>.<tty>.json.
//
// PID is the host-side process that owns the client. The control plane's
// startup sweep (rollout step 4) uses it: a record whose pid is gone but
// whose tty tmux still lists is a client that crashed without detaching, and
// gets detached and deleted then. Nothing here depends on that sweep — a
// stale record is an inert file, never a lock.
type ClientRecord struct {
	Session string `json:"session"`
	TTY     string `json:"tty"`
	PID     int    `json:"pid"`
	At      string `json:"at"`
}

// recordName turns a session and a tty into a file name:
// (cspace-claude, /dev/pts/12) -> cspace-claude.dev-pts-12.json
func recordName(session, tty string) string {
	return session + "." + strings.ReplaceAll(strings.Trim(tty, "/"), "/", "-") + ".json"
}

// Attachment is one live attach into a sandbox's tmux session: the client it
// created, the record file naming it, and the detach that ends it.
type Attachment struct {
	tmux      *Tmux
	container string
	dir       string
	session   string

	lock      *os.File
	lockOnce  sync.Once
	trackStop context.CancelFunc
	trackDone chan struct{}

	mu     sync.Mutex
	tty    string
	closed bool
}

// BeginAttach takes the sandbox's attach lock, snapshots the session's
// current clients, and starts tracking this attach's own client in the
// background. Call it immediately before starting the attaching
// `container exec`, and Close when that child ends.
//
// The lock is a file under ~/.cspace/controlplane/<project>/<sandbox>/
// because the processes that attach to one session are genuinely separate:
// the control plane, and any number of hand-started `cspace attach`es. It is
// flock rather than a pid file so the kernel drops it when its holder dies —
// a crashed attach must not wedge the next one.
//
// An empty session (the no-tmux fallback) yields an inert Attachment: nothing
// is locked, written or detached, and Close succeeds. Callers never nil-check.
func BeginAttach(ctx context.Context, tm *Tmux, container, dir, session string) (*Attachment, error) {
	a := &Attachment{
		tmux:      tm,
		container: container,
		dir:       dir,
		session:   session,
		trackDone: make(chan struct{}),
	}
	if session == "" {
		close(a.trackDone)
		return a, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create control-plane dir %q: %w", dir, err)
	}
	lock, err := lockAttach(ctx, dir)
	if err != nil {
		return nil, err
	}
	a.lock = lock

	before, err := tm.ListClients(ctx, container, session)
	if err != nil {
		a.releaseLock()
		return nil, fmt.Errorf("snapshot tmux clients for %s: %w", container, err)
	}

	// Tracking outlives the caller's ctx on purpose: the attach child runs
	// for as long as the user keeps the window, and cancelling the snapshot
	// context must not stop us identifying the client we have to detach.
	trackCtx, stop := context.WithTimeout(context.Background(), tm.PollFor)
	a.trackStop = stop
	go a.track(trackCtx, before)
	return a, nil
}

// TTY reports this attach's client tty, or "" while it is still unknown.
func (a *Attachment) TTY() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tty
}

// track waits for this attach's client to appear, records it, and releases
// the lock. tmux does not tell a client its own tty and the host-side
// `container exec` cannot ask, so the one tty that was not in the snapshot is
// the answer — which only holds while no other attach can interleave, hence
// the lock held across this whole window.
func (a *Attachment) track(ctx context.Context, before []string) {
	defer close(a.trackDone)
	defer a.releaseLock()
	defer a.trackStop()

	seen := make(map[string]bool, len(before))
	for _, tty := range before {
		seen[tty] = true
	}

	for {
		clients, err := a.tmux.ListClients(ctx, a.container, a.session)
		if err == nil {
			for _, tty := range clients {
				if seen[tty] {
					continue
				}
				a.mu.Lock()
				closed := a.closed
				if !closed {
					a.tty = tty
				}
				a.mu.Unlock()
				if !closed {
					a.writeRecord(tty)
				}
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(a.tmux.PollEvery):
		}
	}
}

// writeRecord publishes the client record atomically: the sweep may read this
// directory at any moment and must never see half a record.
func (a *Attachment) writeRecord(tty string) {
	data, err := json.Marshal(ClientRecord{
		Session: a.session,
		TTY:     tty,
		PID:     os.Getpid(),
		At:      time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return
	}
	path := filepath.Join(a.dir, recordName(a.session, tty))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
	}
}

// Close detaches this attach's tmux client and deletes its record. It is
// idempotent and safe to call when no client was ever identified.
//
// This is the step that makes a closed window actually end the guest-side
// client: the host-side `container exec` dying never reaches tmux, which
// keeps the client attached indefinitely.
func (a *Attachment) Close(ctx context.Context) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	a.mu.Unlock()

	// Stop tracking and wait for it to finish, so the tty it may have just
	// found is visible below. Cancelling first bounds the wait: if the
	// client never appeared, the attach is over and it never will.
	if a.trackStop != nil {
		a.trackStop()
	}
	<-a.trackDone
	a.releaseLock()

	tty := a.TTY()
	if tty == "" {
		return nil
	}
	err := a.tmux.DetachClient(ctx, a.container, tty)
	if rmErr := os.Remove(filepath.Join(a.dir, recordName(a.session, tty))); rmErr != nil &&
		!errors.Is(rmErr, fs.ErrNotExist) && err == nil {
		err = rmErr
	}
	return err
}

func (a *Attachment) releaseLock() {
	a.lockOnce.Do(func() {
		if a.lock == nil {
			return
		}
		_ = syscall.Flock(int(a.lock.Fd()), syscall.LOCK_UN)
		_ = a.lock.Close()
	})
}

// lockAttach takes an exclusive flock on <dir>/attach.lock, retrying until
// the context is done or attachLockTimeout passes. Non-blocking + retry
// rather than a blocking LOCK_EX because a blocking flock cannot be
// cancelled, and a wedged peer must not hang the user's terminal forever.
func lockAttach(ctx context.Context, dir string) (*os.File, error) {
	path := filepath.Join(dir, "attach.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open attach lock %q: %w", path, err)
	}
	deadline := time.Now().Add(attachLockTimeout)
	for {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			return f, nil
		}
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("attach lock %q busy after %s: another attach to this sandbox is starting", path, attachLockTimeout)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd /Users/elliott/Projects/cspace && go test ./internal/control/ -race -count=3 -v`
Expected: PASS for every test, three times over, with no race reports (`-count=3` because the tracking goroutine is the only concurrency in this package and a one-shot pass would hide a flake).

- [ ] **Step 5: Run the full check**

Run: `cd /Users/elliott/Projects/cspace && make check`
Expected: exits 0.

- [ ] **Step 6: Commit**

```bash
cd /Users/elliott/Projects/cspace
git add internal/control/attach.go internal/control/attach_test.go
git commit -m "Track and detach a sandbox's tmux client under an attach lock"
```

---

### Task 8: Run `cspace attach` as a foreground child and detach on exit

**Files:**
- Create: `internal/cli/errors.go`
- Modify: `internal/cli/cmd_attach.go` (rewrite `newAttachCmd` and `attachInteractive`, add `runAttachChild`, add `defaultTmux`)
- Create: `internal/cli/cmd_attach_test.go` (deleted in Task 5; recreated here for the child runner)
- Modify: `internal/cli/cmd_up.go:918` (new `attachInteractive` signature)
- Modify: `internal/cli/tui_actor.go` (`Attach` goes through control)
- Modify: `cmd/cspace/main.go` (honor `ExitError`)
- Modify: `.cspace/context/findings/2026-09-17-attach-orphans-claude-when-the-host-terminal-closes.md` (append a resolved Updates entry)

**Interfaces:**
- Consumes: `control.ClaudeAttach`, `control.AttachArgv`, `control.ControlPlaneDir` (Task 5); `control.NewTmux`, `Tmux.Present` (Task 6); `control.BeginAttach`, `Attachment.Close` (Task 7).
- Produces:
  - `type cli.ExitError struct { Code int }` with `Error() string`
  - `func cli.attachInteractive(ctx context.Context, warn io.Writer, project, sandbox, containerName string, wantTmux bool) error`
  - `func cli.runAttachChild(bin string, argv []string) (exitCode int, err error)`
  - `var cli.defaultTmux = control.NewTmux()`
  - `cspace attach --no-tmux` (hidden flag)

- [ ] **Step 1: Write the failing test**

Create `internal/cli/cmd_attach_test.go` (Task 5 deleted the old one; its argv tests live in `internal/control/argv_test.go` now):

```go
package cli

import (
	"errors"
	"testing"
)

// TestRunAttachChildPropagatesExitStatus — attach stopped being a
// syscall.Exec, so the child's status has to travel back out by hand or an
// interactive `claude` that exits 1 would look like a success.
func TestRunAttachChildPropagatesExitStatus(t *testing.T) {
	code, err := runAttachChild("/bin/sh", []string{"sh", "-c", "exit 7"})
	if err != nil {
		t.Fatalf("runAttachChild() error: %v", err)
	}
	if code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
}

func TestRunAttachChildCleanExit(t *testing.T) {
	code, err := runAttachChild("/bin/sh", []string{"sh", "-c", "exit 0"})
	if err != nil {
		t.Fatalf("runAttachChild() error: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

// TestRunAttachChildReportsStartFailure — a missing binary is cspace's own
// error, not the session's exit status.
func TestRunAttachChildReportsStartFailure(t *testing.T) {
	if _, err := runAttachChild("/nonexistent/container", []string{"container"}); err == nil {
		t.Error("runAttachChild() returned nil for a binary that cannot start")
	}
}

// TestExitErrorCarriesTheCode — main prints nothing for this error and exits
// with the code, so a session that ended 130 (Ctrl-C) does not print
// "Error: exit status 130" over a terminal the user is done with.
func TestExitErrorCarriesTheCode(t *testing.T) {
	var err error = ExitError{Code: 130}
	var target ExitError
	if !errors.As(err, &target) {
		t.Fatal("ExitError is not recoverable with errors.As")
	}
	if target.Code != 130 {
		t.Errorf("Code = %d, want 130", target.Code)
	}
	if target.Error() == "" {
		t.Error("ExitError has an empty message")
	}
}

// TestAttachHasNoTmuxEscapeHatch — tmux by default, with an undocumented way
// out: the flag exists only until no image without tmux is in use.
func TestAttachHasNoTmuxEscapeHatch(t *testing.T) {
	cmd := newAttachCmd()
	flag := cmd.Flags().Lookup("no-tmux")
	if flag == nil {
		t.Fatal("cspace attach has no --no-tmux flag")
	}
	if flag.DefValue != "false" {
		t.Errorf("--no-tmux defaults to %q, want false so attach uses tmux by default", flag.DefValue)
	}
	if !flag.Hidden {
		t.Error("--no-tmux is documented; the design says keep it undocumented")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd /Users/elliott/Projects/cspace && go test ./internal/cli/ -run 'TestRunAttachChild|TestExitError|TestAttachHasNoTmux' -v`
Expected: FAIL to build — `undefined: runAttachChild`, `undefined: ExitError`.

- [ ] **Step 3: Add ExitError and teach main about it**

Create `internal/cli/errors.go`:

```go
package cli

import "fmt"

// ExitError asks main to exit with a specific status and print nothing.
//
// It carries a child process's status out of a command that ran one. An
// interactive `claude` that exits 1, or a session ended with Ctrl-C (130), is
// not a cspace failure to report — it is cspace's own exit code, and printing
// "Error: exit status 130" over a terminal the user has finished with is
// noise.
type ExitError struct{ Code int }

func (e ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }
```

Rewrite `cmd/cspace/main.go`'s `main`:

```go
func main() {
	if err := cli.Execute(); err != nil {
		// A child's exit status is not a cspace error: exit with it silently.
		var exitErr cli.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
```

adding `"errors"` to that file's import block.

- [ ] **Step 4: Rewrite attach**

Replace `newAttachCmd`, `attachInteractive` and `attachArgs` in `internal/cli/cmd_attach.go` with the following (the file's imports become `context`, `errors`, `fmt`, `io`, `os`, `os/exec`, `os/signal`, `syscall`, `time`, `github.com/elliottregan/cspace/internal/control`, `github.com/spf13/cobra` — `sort`, `strings` and the old `syscall.Exec` usage are gone):

```go
// defaultTmux is the process-wide tmux driver: it memoizes the per-sandbox
// presence probe, so the first attach pays for it and nothing else does.
var defaultTmux = control.NewTmux()

func newAttachCmd() *cobra.Command {
	var noTmux bool

	cmd := &cobra.Command{
		Use:   "attach <name>",
		Short: "Open an interactive Claude Code session inside a running sandbox",
		Long: `Drop into an interactive ` + "`claude`" + ` session running inside the named
sandbox. Workspace is /workspace; your turns and the agent's output
appear in your terminal directly.

The session runs inside a tmux session in the sandbox, so closing this
window leaves it running and the next ` + "`cspace attach`" + ` rejoins it
with its screen intact.

This is independent of the supervisor's autonomous session — they
share the same /workspace but are separate Claude Code sessions
with separate context. Use ` + "`cspace send`" + ` to inject turns into the
supervisor's session non-interactively; use ` + "`cspace attach`" + ` for
hands-on work.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureRegistryDaemon(); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: cspace daemon not reachable: %v\n", err)
			}

			name := args[0]
			project := projectName()
			containerName := fmt.Sprintf("cspace-%s-%s", project, name)
			return attachInteractive(cmd.Context(), cmd.ErrOrStderr(), project, name, containerName, !noTmux)
		},
	}

	// Undocumented on purpose (the design's open question 3): it exists only
	// until no sandbox image without tmux is in use, and then it goes.
	cmd.Flags().BoolVar(&noTmux, "no-tmux", false,
		"attach without tmux; the session does not survive this window closing")
	_ = cmd.Flags().MarkHidden("no-tmux")
	return cmd
}

// attachInteractive runs `container exec -it … tmux new-session -A …` as a
// foreground child and, when it ends, detaches the tmux client it created.
//
// It used to syscall.Exec, which was simpler and wrong. A dead host side
// never reaches the guest: the exec'd `claude` was still alive 30 s after its
// host terminal closed, and with tmux the client it left behind stays
// attached indefinitely. Running the exec as a child is what leaves a process
// alive to do the detach — so cspace stays in place, forwards the terminal's
// signals, and exits with the child's status.
// (cs-finding:2026-09-17-attach-orphans-claude-when-the-host-terminal-closes)
func attachInteractive(ctx context.Context, warn io.Writer, project, sandbox, containerName string, wantTmux bool) error {
	useTmux := false
	if wantTmux {
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		useTmux = defaultTmux.Present(probeCtx, containerName)
		cancel()
		if !useTmux {
			_, _ = fmt.Fprintf(warn,
				"warning: this sandbox has no tmux, so the session will not survive this window closing — and `claude` will keep running inside the sandbox when it does. Rebuild the image with `cspace image build`, then `cspace down %s && cspace up %s`.\n",
				sandbox, sandbox)
		}
	}

	spec := control.ClaudeAttach(containerName, useTmux)
	bin, argv, err := control.AttachArgv(spec)
	if err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	att, err := control.BeginAttach(ctx, defaultTmux, containerName,
		control.ControlPlaneDir(home, project, sandbox), spec.Session)
	if err != nil {
		return err
	}

	// Clear the terminal before claude takes over so the user gets a
	// clean screen instead of opening claude on top of their pre-
	// cspace-up shell history. \033c is the full reset (clear screen +
	// scrollback + cursor home + reset attributes); claude immediately
	// repaints over it. Stdout-only — stderr stays usable for diagnostics.
	if isStdoutTTY() {
		_, _ = os.Stdout.WriteString("\033c")
	}

	code, runErr := runAttachChild(bin, argv)

	// The detach gets its own context: the caller's may already be cancelled
	// by whatever ended the session, and this is the one thing that must
	// still run.
	detachCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if closeErr := att.Close(detachCtx); closeErr != nil {
		_, _ = fmt.Fprintf(warn, "warning: detaching this sandbox's tmux client failed: %v\n", closeErr)
	}

	if runErr != nil {
		return runErr
	}
	if code != 0 {
		return ExitError{Code: code}
	}
	return nil
}

// runAttachChild runs the attach argv wired straight to this process's
// terminal and reports the child's exit status.
//
// The child shares stdin/stdout/stderr — the real tty — so `container exec
// -it` puts that terminal into raw mode itself and keystrokes reach the guest
// as bytes rather than as host-side signals. The signals we do get are
// forwarded rather than acted on: SIGINT and SIGTERM belong to the session
// inside, SIGWINCH tells the container CLI to re-read the window size (host
// pty resizes propagate into the guest from there), and SIGHUP means the
// window is gone, so the child is ended and the caller's detach runs. Without
// the Notify below, Go's default SIGINT handling would kill cspace out from
// under the child and skip the detach entirely — which is the bug this whole
// change exists to fix.
func runAttachChild(bin string, argv []string) (int, error) {
	child := exec.Command(bin, argv[1:]...)
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr

	sigs := make(chan os.Signal, 8)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGWINCH, syscall.SIGHUP)
	defer signal.Stop(sigs)

	if err := child.Start(); err != nil {
		return 0, fmt.Errorf("start %s: %w", bin, err)
	}

	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case sig := <-sigs:
				if sig == syscall.SIGHUP {
					// Nothing can be typed into the child any more. Ask it to
					// go, then insist, so Wait returns and the detach runs
					// while the container is still reachable.
					_ = child.Process.Signal(syscall.SIGHUP)
					time.AfterFunc(2*time.Second, func() { _ = child.Process.Kill() })
					continue
				}
				_ = child.Process.Signal(sig)
			}
		}
	}()

	err := child.Wait()
	close(done)

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return 0, fmt.Errorf("attach to sandbox: %w", err)
	}
	return 0, nil
}
```

(Task 5 already removed `attachArgs`, so there is no adapter left to reconcile — `attachInteractive` builds the spec itself because it needs `spec.Session` for `BeginAttach`.)

- [ ] **Step 5: Update the two other callers**

In `internal/cli/cmd_up.go`, the auto-attach at the end of `up`'s RunE:

```go
				return attachInteractive(cmd.Context(), cmd.ErrOrStderr(), project, name, containerName, true)
```

In `internal/cli/tui_actor.go`, replace `Attach` (this file is deleted in rollout step 3; it changes now so the v1 dashboard joins the same tmux session instead of starting a rival `claude`):

```go
// Attach joins the sandbox's tmux session through the same control-plane path
// `cspace attach` uses, so the two share one session rather than running two
// claudes against one workspace. tea.ExecProcess suspends the dashboard and
// runs the exec in the foreground; its callback is where the detach happens.
func (t *tuiActor) Attach(row tui.Row) tea.Cmd {
	ctx := context.Background()
	spec := control.ClaudeAttach(row.Container, defaultTmux.Present(ctx, row.Container))
	bin, argv, err := control.AttachArgv(spec)
	if err != nil {
		return func() tea.Msg { return tui.Result("attach", err) }
	}
	att, err := control.BeginAttach(ctx, defaultTmux, row.Container,
		control.ControlPlaneDir(t.home, row.Project, row.Name), spec.Session)
	if err != nil {
		return func() tea.Msg { return tui.Result("attach", err) }
	}
	cmd := exec.Command(bin, argv[1:]...)
	return tea.ExecProcess(cmd, func(execErr error) tea.Msg {
		closeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if closeErr := att.Close(closeCtx); closeErr != nil && execErr == nil {
			execErr = closeErr
		}
		return tui.Result("attach", execErr)
	})
}
```

adding `github.com/elliottregan/cspace/internal/control` to that file's imports (`context` and `time` are already there).

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd /Users/elliott/Projects/cspace && make test`
Expected: PASS across `./...`, including the new `TestRunAttachChildPropagatesExitStatus`, `TestRunAttachChildCleanExit`, `TestRunAttachChildReportsStartFailure`, `TestExitErrorCarriesTheCode` and `TestAttachHasNoTmuxEscapeHatch`.

- [ ] **Step 7: Verify attach no longer syscall.Execs and go.mod is untouched**

Run:
```bash
cd /Users/elliott/Projects/cspace && grep -rn "syscall\.Exec(" internal/ --include='*.go'; git diff --stat go.mod go.sum
```
Expected: no output from either — no `syscall.Exec` call remains anywhere in `internal/`, and no dependency changed.

The grep matches the call, not the name: both `cmd_attach.go` and `cmd_attach_test.go` still say "syscall.Exec" in prose explaining what attach stopped doing, and a bare `grep "syscall.Exec"` would report those two comments as failures.

- [ ] **Step 8: Resolve the finding**

Append to `.cspace/context/findings/2026-09-17-attach-orphans-claude-when-the-host-terminal-closes.md`, under `## Updates`, and change the frontmatter `status: open` to `status: resolved`:

```markdown
### 2026-09-17 — status: resolved
`cspace attach` now runs `container exec` as a foreground child, forwards
SIGINT/SIGTERM/SIGWINCH and ends the child on SIGHUP, and detaches its tmux
client (identified as the one new tty across the attach, under a per-sandbox
flock) before returning the child's exit status. Sandboxes built from an
image without tmux still fall back to the direct exec and now warn that the
session will be left behind.
```

- [ ] **Step 9: Run the full check**

Run: `cd /Users/elliott/Projects/cspace && make check`
Expected: exits 0.

- [ ] **Step 10: Commit**

```bash
cd /Users/elliott/Projects/cspace
git add internal/cli/cmd_attach.go internal/cli/cmd_attach_test.go internal/cli/errors.go \
        internal/cli/cmd_up.go internal/cli/tui_actor.go cmd/cspace/main.go \
        .cspace/context/findings/2026-09-17-attach-orphans-claude-when-the-host-terminal-closes.md
git commit -m "Run attach as a child and detach its tmux client on exit (cs-finding:2026-09-17-attach-orphans-claude-when-the-host-terminal-closes)"
```

---

### Task 9: Rebuild the image and verify the sandbox side by hand

Apple Container is not available in CI and `cspace up` boots a real microVM, so this task is run **by a human on a Mac with Apple Container**, from the repo checkout. It is the only proof that the image work landed — every earlier task tested text, not a running sandbox.

**Files:** none modified. This is verification.

**Interfaces:**
- Consumes: everything from Tasks 2-8.
- Produces: nothing. If a check fails, fix it in the task that owns it and re-run.

- [ ] **Step 1: Build the CLI and the image**

Run:
```bash
cd /Users/elliott/Projects/cspace && make build && make cspace-image
```
Expected: `bin/cspace-go` builds, then `container build` finishes with `Successfully tagged cspace:latest` (or the equivalent success line). A failure of the `RUN test -f /usr/local/etc/cspace-tmux.conf` step means the COPY line is missing.

- [ ] **Step 2: Boot a throwaway sandbox**

Run:
```bash
cd /Users/elliott/Projects/cspace && ./bin/cspace-go up tmuxcheck --no-attach
```
Expected: the boot completes and prints the `attach:` / `browse:` summary lines. The container is `cspace-<project>-tmuxcheck`; in this checkout the project is `cspace`, so the steps below say `cspace-cspace-tmuxcheck` — substitute if `.cspace.json` names it something else.

- [ ] **Step 3: Verify tmux and its config are in the image**

Run:
```bash
container exec cspace-cspace-tmuxcheck sh -c 'command -v tmux && tmux -V && grep -c extended-keys /usr/local/etc/cspace-tmux.conf'
```
Expected: `/usr/bin/tmux`, then `tmux 3.3a`, then `1`.

- [ ] **Step 4: Verify tmux accepts the config end to end**

Run:
```bash
container exec cspace-cspace-tmuxcheck tmux -f /usr/local/etc/cspace-tmux.conf new-session -d -s conftest \
  && container exec cspace-cspace-tmuxcheck tmux show-options -g extended-keys \
  && container exec cspace-cspace-tmuxcheck tmux kill-session -t conftest
```
Expected: no error output from the first command (a rejected option prints `unknown option` and exits non-zero), then `extended-keys always`.

- [ ] **Step 5: Verify the hooks are seeded**

Run:
```bash
container exec cspace-cspace-tmuxcheck jq -c '.hooks | keys' /home/dev/.claude/settings.json
container exec cspace-cspace-tmuxcheck sh -c 'printf "{\"session_id\":\"s1\",\"hook_event_name\":\"Stop\"}" | /usr/local/bin/cspace-agent-state.sh idle && cat /sessions/agent-state.json'
```
Expected: the nine event names, then `{"state":"idle","at":"…Z","session_id":"s1","event":"Stop"}`.

- [ ] **Step 6: Verify the host sees the same file**

Run:
```bash
cat ~/.cspace/sessions/cspace/tmuxcheck/agent-state.json
```
Expected: byte-identical to what the sandbox printed — the bind mount, not a copy.

- [ ] **Step 7: Verify attach, detach, and reattach**

In terminal A run `cd /Users/elliott/Projects/cspace && ./bin/cspace-go attach tmuxcheck` and wait for the Claude prompt. In terminal B run:
```bash
container exec cspace-cspace-tmuxcheck tmux list-clients -t cspace-claude -F '#{client_tty}'
ls ~/.cspace/controlplane/cspace/tmuxcheck/
```
Expected: exactly one tty, and a record file named `cspace-claude.<tty>.json` beside `attach.lock`.

Now close terminal A's window (not Ctrl-C — close it, so the process gets SIGHUP). Back in terminal B:
```bash
container exec cspace-cspace-tmuxcheck tmux list-clients -t cspace-claude -F '#{client_tty}'
container exec cspace-cspace-tmuxcheck tmux list-sessions
ls ~/.cspace/controlplane/cspace/tmuxcheck/
```
Expected: **no** clients listed; `cspace-claude: 1 windows …` still listed (the session survived); the record file gone, `attach.lock` still there.

Then reattach: `./bin/cspace-go attach tmuxcheck`.
Expected: the previous conversation is on screen — the same session, redrawn, not a fresh `claude`.

- [ ] **Step 8: Verify two concurrent attaches**

With the attach from step 7 still open in terminal A, run `./bin/cspace-go attach tmuxcheck` in terminal C.
Expected: both terminals show the same screen; `container exec cspace-cspace-tmuxcheck tmux list-clients -t cspace-claude -F '#{client_tty}'` lists two ttys and `ls ~/.cspace/controlplane/cspace/tmuxcheck/` shows two record files. Closing terminal C removes exactly its own tty and its own record, leaving A attached.

- [ ] **Step 9: Verify exit-status propagation and the escape hatch**

Run:
```bash
cd /Users/elliott/Projects/cspace && ./bin/cspace-go attach tmuxcheck --no-tmux
```
Expected: a warning-free direct session (no tmux argv); type `/exit` and confirm the shell prompt returns. Then `echo $?` — expected `0`, with no `Error:` line printed.

- [ ] **Step 10: Tear down**

Run:
```bash
cd /Users/elliott/Projects/cspace && ./bin/cspace-go down tmuxcheck && rm -rf ~/.cspace/controlplane/cspace/tmuxcheck
```
Expected: the sandbox is removed; the leftover `attach.lock` directory is cleaned up by hand (`cspace down` wipes the session dir, not the control-plane dir — the control plane's own sweep in rollout step 4 is what will retire these).

- [ ] **Step 11: Commit (only if something needed fixing)**

If steps 1-10 all passed, there is nothing to commit — say so and stop. If a fix was needed, commit it against the task that owns the file:

```bash
cd /Users/elliott/Projects/cspace
git add -A
git commit -m "Fix <what the manual verification found>"
```

---

## Self-review notes

Checked against the spec's "Sandbox side" section, the `cspace attach` change, the two findings and the no-tmux fallback:

- **Image** — Task 2 (tmux + `tmux.conf` + `sync-embedded` rule + per-file COPY guard), Task 3 (`cspace-agent-state.sh` COPY). The spec says the script "is already swept by the `lib/runtime/scripts/*.sh` glob" — confirmed against the Makefile, so no sync rule is needed for it, only the `tmux.conf` one.
- **Sessions** — Task 5 (`SessionClaude`/`SessionShell` constants, the attach-or-create argv with `-A`, `-c /workspace`). `SessionShell` is defined and argv-tested here even though no shell pane exists until rollout step 4, because the constant is what makes "one session per pane kind" true rather than aspirational, and the argv builder is generic anyway.
- **Detach protocol** — Tasks 6-8 cover items 1-3 (lock, snapshot/identify/record, detach-and-delete on close). Item 4 (the startup sweep) is explicitly out of scope: it runs at control-plane startup, which rollout step 4 builds. Recorded in "Out of scope" with the reason.
- **Agent state hooks** — Tasks 3 and 4; all nine rows of the spec's table are asserted by name, state and matcher in `cspace-entrypoint.test.sh`, plus the "no event has two entries" invariant the spec calls out.
- **Fallback** — `Tmux.Present` (Task 6, memoized per sandbox, exactly the `sh -c 'command -v tmux'` probe), the `Session == ""` argv branch (Task 5), the warning naming `cspace image build` (Task 8), and `--no-tmux` hidden (Task 8, the spec's open question 3 default).
- **Stale-image prompt** — the spec's "the stale-image gate in `cspace up` prompts for the rebuild as it does for any image bump" needs no work: `preflightImageGate` already compares `cspace:latest`'s `cspace.version` label against the running CLI, and rebuilding for this change bumps that label like any other. No task touches it, deliberately.
- **Findings** — Task 1 files both; Task 8 resolves the first with the `(cs-finding:…)` commit convention.
- No task adds a Go dependency; Tasks 5 and 8 both verify `go.mod`/`go.sum` are untouched.
