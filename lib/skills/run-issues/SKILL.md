---
name: run-issues
description: Run a list of GitHub issues using autonomous cspace devcontainer agents. Use when the user says "run these issues", "tackle issues 281 282 283", "have agents work on these", or provides a list of issue numbers to process.
user-invocable: true
---

# Run Issues

Hand a list of GitHub issues to the cspace coordinator. The coordinator's playbook (`/opt/cspace/lib/agents/coordinator.md`) handles dependency resolution, base-branch computation, grouping, warming, launching, watchdog, and final review — this skill is just the entrypoint.

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

### Step 1: Parse the issue list

Extract issue numbers from the user's message and confirm with a one-line summary per issue:

```bash
for n in <issue-numbers>; do
  gh issue view $n --json number,title,state --jq '"#\(.number) \(.title) [\(.state)]"'
done
```

If any are already closed, surface that and ask whether to skip them.

### Step 2: Feature branch decision

- **Default: no feature branch.** Each issue creates its own PR targeting main.
- **Ask for a feature branch** only if the user hints at it, the issues share a milestone, or they have inter-dependencies.

Don't scan for shared files or dependency lines yourself — the coordinator reads each issue body and builds a dependency graph as Phase 0 of its playbook. Duplicating that work here just wastes turns.

### Step 3: Launch the coordinator

Write the instruction prompt to a file (always — never inline):

```bash
cat > /tmp/coord-issues.txt <<'PROMPT'
Work on these GitHub issues, each independently targeting main:
#538, #537, #536, #519

Each issue gets its own container and PR. Merge order does not matter.
PROMPT

cspace coordinate --prompt-file /tmp/coord-issues.txt
```

Run it in the background with `run_in_background: true` and a 60-minute timeout.

The instruction prompt should include:
- The list of issue numbers
- The base branch (main, or the feature branch from Step 2)
- Anything the user mentioned that isn't self-evident from the issues (e.g., "skip E2E for these", "merge into feature/foo", "stop if #281 fails")

**Example — feature branch with sequencing hint:**
```
Work on these issues targeting feature/interview-improvements:
#535, #540

Follow the coordinator playbook. If you discover #540 needs #535's changes, sequence them yourself.
```

### Step 4: Monitor and report

When the coordinator completes, read its output and present the final summary. If it failed, show the error — the coordinator is resumable, so re-running with the same `--name` picks up where it left off.
