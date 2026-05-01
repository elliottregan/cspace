#!/usr/bin/env bash
# Spike: per-sandbox git clone bind-mounted as /workspace.
#
# Goal: prove that the per-sandbox-clone design from the workspace mount
# discussion delivers the properties we agreed on:
#   1. /workspace inside the sandbox is the MAIN worktree of a normal git
#      clone (not a `git worktree`-style sub-worktree).
#   2. Files written/committed inside the sandbox land on the host
#      filesystem instantly via the bind mount.
#   3. The host can `git fetch` the sandbox's branch into the project's
#      main repo without a network round-trip.
#   4. Sandboxes don't share filesystem state with each other or the
#      project's working tree.
#
# Pre-P1 the prototype-up command grew a temporary --workspace flag; the
# clone provisioning here is done by this spike script. P1's cspace2-up
# absorbs both responsibilities.
#
# ── POC concessions (acknowledged, polished in P1) ────────────────────────
#   - Hardcoded sandbox name "wstest"; concurrent runs collide.
#   - Clone path layout (~/.cspace/clones/<project>/<sandbox>) is the
#     proposed P1 default, but P1 will own the path policy.
#   - `git clone` runs synchronously on the host before the sandbox boots.
#     For big-history projects this can be slow; --shared cloning is a
#     P1 optimization (with the GC caveat to address).
#   - Commits inside the sandbox use a hand-rolled `git -c user.email=...
#     -c user.name=...` so we don't depend on a Claude session being able
#     to run git config. With Claude auth working (see 2026-05-01-claude-auth.sh)
#     the agent could do it itself.
#   - Cleanup leaves the clone on disk by default to mirror the proposed
#     P1 cspace2-down semantic (don't lose work on shutdown). Set
#     KEEP_CLONE=0 to remove it after a successful run.
# ───────────────────────────────────────────────────────────────────────────
set -euo pipefail

CSPACE="${CSPACE:-./bin/cspace-go}"
SANDBOX="${SANDBOX:-wstest}"
KEEP_CLONE="${KEEP_CLONE:-1}"
PROJECT_ROOT="$(git rev-parse --show-toplevel)"
PROJECT_NAME="$(basename "$PROJECT_ROOT")"
CLONE_BASE="$HOME/.cspace/clones/$PROJECT_NAME"
CLONE_DIR="$CLONE_BASE/$SANDBOX"
BRANCH="cspace/$SANDBOX"
container="cspace-${PROJECT_NAME}-${SANDBOX}"

if [[ ! -x "$CSPACE" ]]; then
  echo "build cspace-go first: make build" >&2
  exit 1
fi

cleanup_on_error() {
  local rc="$?"
  if [[ "$rc" -ne 0 ]]; then
    echo "FAIL — leaving $container up and clone at $CLONE_DIR for inspection." >&2
    echo "    container exec $container sh" >&2
    echo "    ls -la $CLONE_DIR" >&2
    echo "    $CSPACE prototype-down $SANDBOX" >&2
  fi
}
trap cleanup_on_error EXIT

echo "==> project root: $PROJECT_ROOT"
echo "==> clone path:   $CLONE_DIR"
echo "==> branch:       $BRANCH"

# Idempotent: nuke any prior clone for this sandbox so each run is fresh.
if [[ -d "$CLONE_DIR" ]]; then
  echo "==> removing prior clone at $CLONE_DIR"
  rm -rf "$CLONE_DIR"
fi
mkdir -p "$CLONE_BASE"

echo "==> cloning project root into $CLONE_DIR"
git clone "$PROJECT_ROOT" "$CLONE_DIR" >/dev/null
( cd "$CLONE_DIR" && git checkout -b "$BRANCH" >/dev/null 2>&1 )

# Ensure no leftover sandbox from a previous run.
"$CSPACE" prototype-down "$SANDBOX" >/dev/null 2>&1 || true

echo "==> bringing up sandbox with --workspace=$CLONE_DIR"
"$CSPACE" prototype-up "$SANDBOX" --workspace "$CLONE_DIR" >/dev/null
sleep 3

# 1. Verify /workspace inside the sandbox is the MAIN worktree of a clone.
echo "==> verifying /workspace is a normal main-worktree clone"
inside_view=$(container exec "$container" sh -c '
  cd /workspace || exit 1
  echo "--- pwd ---"
  pwd
  echo "--- ls -la .git | head ---"
  ls -la .git | head -3
  echo "--- git rev-parse ---"
  echo "  is-inside-work-tree: $(git rev-parse --is-inside-work-tree)"
  echo "  is-inside-git-dir:   $(git rev-parse --is-inside-git-dir)"
  echo "  git-dir:             $(git rev-parse --git-dir)"
  echo "  show-toplevel:       $(git rev-parse --show-toplevel)"
  echo "  current branch:      $(git rev-parse --abbrev-ref HEAD)"
') || { echo "FAIL: container exec failed" >&2; exit 1; }
echo "$inside_view"

# .git must be a DIRECTORY (normal repo), not a FILE (worktree-style pointer).
git_dir_kind=$(container exec "$container" stat -c '%F' /workspace/.git 2>/dev/null || echo "missing")
if [[ "$git_dir_kind" != "directory" ]]; then
  echo "FAIL: /workspace/.git is '$git_dir_kind', expected 'directory'" >&2
  exit 1
fi

# Branch must be cspace/<sandbox>.
sandbox_branch=$(container exec "$container" sh -c 'cd /workspace && git rev-parse --abbrev-ref HEAD')
if [[ "$sandbox_branch" != "$BRANCH" ]]; then
  echo "FAIL: expected branch '$BRANCH' inside sandbox, got '$sandbox_branch'" >&2
  exit 1
fi

# 2. Have the sandbox make a commit. Verify the host sees it instantly.
echo "==> sandbox makes a commit"
probe_file="spike-touch-${RANDOM}.txt"
probe_content="hello from sandbox $(date -u +%FT%TZ)"
container exec "$container" sh -c "
  set -e
  cd /workspace
  echo '$probe_content' > '$probe_file'
  git -c user.email=agent@cspace -c user.name='cspace agent' add '$probe_file'
  git -c user.email=agent@cspace -c user.name='cspace agent' commit -m 'spike: agent commit from sandbox $SANDBOX'
" >/dev/null

# Host filesystem should see the new file at the bind-mount path with no sync.
if [[ ! -f "$CLONE_DIR/$probe_file" ]]; then
  echo "FAIL: probe file '$probe_file' not visible on host at $CLONE_DIR" >&2
  exit 1
fi
host_content=$(cat "$CLONE_DIR/$probe_file")
if [[ "$host_content" != "$probe_content" ]]; then
  echo "FAIL: host file content mismatch ('$host_content' vs '$probe_content')" >&2
  exit 1
fi
echo "==> host sees probe file at $CLONE_DIR/$probe_file with expected content"

# Host's view of the clone shows the commit immediately.
host_log=$(cd "$CLONE_DIR" && git log -1 --pretty=format:'%h %s')
case "$host_log" in
  *"spike: agent commit from sandbox $SANDBOX"*)
    echo "==> host sees commit: $host_log"
    ;;
  *)
    echo "FAIL: host clone's git log doesn't show the agent commit (got: $host_log)" >&2
    exit 1
    ;;
esac

# 3. The project's main repo can fetch the sandbox's branch with no network round-trip.
echo "==> fetching sandbox branch into project main repo"
( cd "$PROJECT_ROOT" && git fetch "$CLONE_DIR" "$BRANCH:refs/cspace/spike-fetched-$SANDBOX" >/dev/null )
fetched_log=$(cd "$PROJECT_ROOT" && git log -1 --pretty=format:'%h %s' "refs/cspace/spike-fetched-$SANDBOX")
case "$fetched_log" in
  *"spike: agent commit from sandbox $SANDBOX"*)
    echo "==> project main repo has agent's branch: $fetched_log"
    ;;
  *)
    echo "FAIL: project main repo couldn't fetch agent's branch (got: $fetched_log)" >&2
    exit 1
    ;;
esac
# Clean up the temporary ref so we don't leave it lying around.
( cd "$PROJECT_ROOT" && git update-ref -d "refs/cspace/spike-fetched-$SANDBOX" )

# 4. Project main repo's working tree must NOT have been touched.
if [[ -f "$PROJECT_ROOT/$probe_file" ]]; then
  echo "FAIL: probe file leaked into project main worktree at $PROJECT_ROOT/$probe_file" >&2
  exit 1
fi
echo "==> project main worktree was NOT touched (isolation verified)"

# All checks passed. Tear down.
trap - EXIT
"$CSPACE" prototype-down "$SANDBOX" >/dev/null
echo "==> sandbox $SANDBOX torn down"
if [[ "$KEEP_CLONE" == "1" ]]; then
  echo "==> keeping clone at $CLONE_DIR (set KEEP_CLONE=0 to remove)"
else
  rm -rf "$CLONE_DIR"
  echo "==> clone removed"
fi
echo "==> PASS"
