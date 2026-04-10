---
name: run-ready
description: Run all GitHub issues labeled "ready" using autonomous cspace devcontainer agents. Use when the user says "run ready issues", "run ready", "tackle ready issues", or wants to process all issues marked ready for implementation.
user-invocable: true
---

# Run Ready Issues

Hand every open issue labeled `ready` to the cspace coordinator. The coordinator's playbook (`/opt/cspace/lib/agents/coordinator.md`) handles dependency resolution, base-branch computation, grouping, warming, launching, watchdog, and final review — this skill is just the entrypoint.

The label name comes from `.cspace.json` (`agent.issue_label`, default `"ready"`).

## Gate: do these issues need containers?

Before reaching for `cspace coordinate`, check whether the issues actually
need environment isolation (databases, browser sessions, running services).
If the issues are pure code generation, refactoring, or writing new files
that can be verified with a build command — use worktree-isolated subagents
via the `dispatching-parallel-agents` skill instead. It's faster and simpler.

**Use cspace coordinate when:** issues need E2E tests, database migrations,
dev servers, or any running services to verify.

**Use worktree subagents when:** issues are pure code changes verifiable with
build/lint/unit-test commands and don't need a running environment.

## Process

### Step 1: Fetch ready issues

```bash
LABEL=$(jq -r '.agent.issue_label // "ready"' .cspace.json 2>/dev/null || echo "ready")

gh issue list --label "$LABEL" --state open --json number,title,labels,milestone \
  --jq '.[] | "#\(.number) \(.title) [\(.labels | map(.name) | join(", "))]"'
```

Present the list. If empty, tell the user and stop.

### Step 2: Feature branch decision

- **Default: no feature branch.** Each issue creates its own PR targeting main.
- **Ask for a feature branch** only if the user hints at it, the issues share a milestone, or they have inter-dependencies.

Don't scan for blockers, shared files, or open PRs yourself — the coordinator reads each issue body and builds a dependency graph as Phase 0 of its playbook.

### Step 3: Launch the coordinator

Write the instruction prompt to a file (always — never inline):

```bash
cat > /tmp/coord-ready.txt <<'PROMPT'
Work on these ready-labeled issues, each independently targeting main:
#538, #537, #536, #519

Follow the coordinator playbook. Each gets its own container and PR. Merge order does not matter.
PROMPT

cspace coordinate --prompt-file /tmp/coord-ready.txt
```

Run in the background with `run_in_background: true` and a 60-minute timeout.

The instruction prompt should include:
- The list of ready issue numbers
- The base branch (main, or the feature branch from Step 2)
- Anything the user mentioned that isn't self-evident (e.g., "skip E2E", "stop on first failure")

### Step 4: Monitor and report

When the coordinator completes, read its output and present the final summary. If it failed, show the error — the coordinator is resumable, so re-running with the same `--name` picks up where it left off.

### Step 5: Clean up labels

After all PRs are created, offer to remove the `ready` label from the merged issues:

```bash
gh issue edit <N> --remove-label "ready"
```
