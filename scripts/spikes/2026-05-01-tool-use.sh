#!/usr/bin/env bash
# Spike: verify Claude actually USES its tools (Bash, Read, Write) inside a
# cspace sandbox. Plumbing-only proofs from prior spikes verified the wiring
# but not whether tool invocations fire and produce real side-effects.
#
# Builds on top of the workspace-clone design from
# scripts/spikes/2026-05-01-workspace-clone.sh: sandbox /workspace is a real
# cspace clone, so the agent can read/write project files, and host can
# verify side-effects via the bind mount.
#
# Three turns, three tools:
#   1. Read tool: ask the agent to read /workspace/README.md and report a
#      heading. Verify a tool_use(name=Read) event landed AND the assistant
#      actually quoted the file.
#   2. Bash tool: ask the agent to run `pwd && ls /workspace | head` and
#      report. Verify a tool_use(name=Bash) event with the expected command.
#   3. Write tool: ask the agent to create /workspace/touched-by-agent.txt
#      with a known phrase. Verify a tool_use(name=Write) event AND the
#      file actually exists on the host filesystem with the right content.
#
# ── POC concessions (acknowledged, polished in P1) ────────────────────────
#   - Hardcoded sandbox name "tool-test"; concurrent runs collide.
#   - Reuses the workspace-clone scaffolding inline rather than calling out
#     to a shared helper. Polish into a shared bash function later.
#   - Probe phrase for the Write check is fixed; doesn't randomize.
#   - Long inter-turn sleeps (12s) to give Claude time to think + call tools.
#     Not robust against API slowness; a real test would poll for a result
#     event with a timeout.
#   - Doesn't verify EVERY tool, just the three most-used. Skill, Glob,
#     Grep, WebFetch etc. are presumed correct if these three work since
#     they share registration plumbing.
# ───────────────────────────────────────────────────────────────────────────
set -euo pipefail

CSPACE="${CSPACE:-./bin/cspace-go}"
SANDBOX="${SANDBOX:-tool-test}"
PROJECT_ROOT="$(git rev-parse --show-toplevel)"
PROJECT_NAME="$(basename "$PROJECT_ROOT")"
CLONE_BASE="$HOME/.cspace/clones/$PROJECT_NAME"
CLONE_DIR="$CLONE_BASE/$SANDBOX"
BRANCH="cspace/$SANDBOX"
container="cspace-${PROJECT_NAME}-${SANDBOX}"
WRITE_PROBE="cspace-tool-spike-$(date -u +%s)"

if [[ ! -x "$CSPACE" ]]; then
  echo "build cspace-go first: make build" >&2
  exit 1
fi

cleanup_on_error() {
  local rc="$?"
  if [[ "$rc" -ne 0 ]]; then
    echo "FAIL — leaving $container up and clone at $CLONE_DIR for inspection." >&2
    echo "    container exec $container cat /sessions/primary/events.ndjson | tail -40" >&2
    echo "    $CSPACE prototype-down $SANDBOX" >&2
  fi
}
trap cleanup_on_error EXIT

echo "==> setting up clone at $CLONE_DIR"
[[ -d "$CLONE_DIR" ]] && rm -rf "$CLONE_DIR"
mkdir -p "$CLONE_BASE"
git clone "$PROJECT_ROOT" "$CLONE_DIR" >/dev/null
( cd "$CLONE_DIR" && git checkout -b "$BRANCH" >/dev/null 2>&1 )

# Ensure no leftover sandbox.
"$CSPACE" prototype-down "$SANDBOX" >/dev/null 2>&1 || true

echo "==> bringing up sandbox with --workspace=$CLONE_DIR"
"$CSPACE" prototype-up "$SANDBOX" --workspace "$CLONE_DIR" >/dev/null
sleep 4

# Turn 1: Read tool
echo "==> turn 1: Read"
"$CSPACE" prototype-send "$SANDBOX" \
  "Read /workspace/CLAUDE.md and tell me the very first heading verbatim. Use the Read tool to do it. Reply with only the heading text, no commentary." >/dev/null

# Turn 2: Bash tool — wait for previous turn to complete first.
sleep 14
echo "==> turn 2: Bash"
"$CSPACE" prototype-send "$SANDBOX" \
  "Run the bash command 'ls /workspace | wc -l' and report only the resulting number. Use the Bash tool." >/dev/null

# Turn 3: Write tool
sleep 14
echo "==> turn 3: Write"
"$CSPACE" prototype-send "$SANDBOX" \
  "Use the Write tool to create the file /workspace/touched-by-agent.txt with exactly this single line of content (no trailing newline issues, no extra commentary): $WRITE_PROBE" >/dev/null

# Wait for the third turn to complete.
sleep 18

echo "==> inspecting events.ndjson"
parser="$(dirname "$0")/2026-05-01-tool-use.py"
verdict=$(container exec "$container" cat /sessions/primary/events.ndjson | python3 "$parser" "$WRITE_PROBE")
echo "$verdict"

# Side-effect proof: verify the Write tool actually wrote the file on the host.
echo "==> side-effect check: host file at $CLONE_DIR/touched-by-agent.txt"
if [[ ! -f "$CLONE_DIR/touched-by-agent.txt" ]]; then
  echo "FAIL: agent claimed to write but file is not on host filesystem" >&2
  exit 1
fi
host_content=$(cat "$CLONE_DIR/touched-by-agent.txt")
case "$host_content" in
  *"$WRITE_PROBE"*)
    echo "==> host sees the agent-written content: $host_content"
    ;;
  *)
    echo "FAIL: file exists but content '$host_content' missing probe '$WRITE_PROBE'" >&2
    exit 1
    ;;
esac

# Final pass/fail: parser exit code drove the verdict already; if we got here
# without exit, parser said PASS and side-effect check passed.
if container exec "$container" cat /sessions/primary/events.ndjson | python3 "$parser" "$WRITE_PROBE" >/dev/null; then
  trap - EXIT
  "$CSPACE" prototype-down "$SANDBOX" >/dev/null
  rm -rf "$CLONE_DIR"
  echo "==> sandbox $SANDBOX torn down, clone removed"
  echo "==> PASS"
else
  exit 1
fi
