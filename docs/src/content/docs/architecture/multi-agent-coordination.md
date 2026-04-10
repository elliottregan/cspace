---
title: Multi-Agent Coordination
description: How the coordinator manages parallel agents, dependency graphs, merge ordering, and conflict resolution across multiple GitHub issues.
sidebar:
  order: 3
---

When you have multiple related GitHub issues to resolve, the coordinator agent manages them in parallel. It launches one autonomous agent per issue, tracks dependencies between them, merges PRs in the right order, and handles conflicts — all without human intervention.

The coordinator runs inside its own devcontainer instance and uses the implementer playbook as a template for each agent it launches.

## Coordinator phases

### Phase 0 — Feature branch and dependency graph

The coordinator first determines the base branch strategy and builds a dependency graph.

**Feature branch**: If the issues share a milestone or are related feature work, the coordinator creates a feature branch (e.g., `feature/login`) from `main`. All issue branches target this feature branch instead of `main`, keeping work-in-progress off the main branch until the full feature is ready.

**Dependency graph**: The coordinator reads all issues and builds a graph from `blocked-by: #N` annotations in issue descriptions. This graph drives the launch order — agents for blocked issues wait until their dependencies are resolved.

### Phase 1 — Setup

The coordinator pre-provisions all containers upfront using `cspace warm`:

```bash
cspace warm issue-10 issue-11 issue-12 issue-13
```

This creates all containers — even for blocked issues — before any agents start. Pre-provisioning avoids setup delays when a blocked issue becomes unblocked (its container is already warm and ready).

If any containers fail validation, the coordinator destroys and retries them.

### Phase 2 — Render and launch

For each unblocked issue, the coordinator:

1. **Renders the implementer prompt** — substitutes template variables (`${NUMBER}`, `${BASE_BRANCH}`, `${VERIFY_COMMAND}`, `${E2E_COMMAND}`) with issue-specific values
2. **Launches the agent** — `cspace up issue-<N> --base <branch> --prompt-file /tmp/implementer-<N>.txt`

All ready agents launch in a single batch. Blocked agents wait.

> A maximum of **4 parallel agents** run at once to avoid overloading the host. If more than 4 issues are unblocked, the coordinator batches them — launching the first 4, waiting for any to complete, then launching the next.

### Phase 3 — Monitor

The coordinator does not poll. It waits for background task completion notifications.

When an agent completes, the coordinator:

1. Reads the full output to extract the PR URL, pass/fail status, and session ID
2. Reports the completion immediately (does not batch notifications)
3. Dispatches a code review in the same container via `cspace send`
4. Verifies acceptance criteria by reading the issue and PR diff

### Phase 4 — Iterate

For agents that failed (non-zero exit, no PR, or errors in output):

1. Read the output to diagnose the root cause
2. Send a targeted follow-up via `cspace send issue-<N> "<fix instructions>"`
3. If the session is dead, re-render the prompt and re-launch from scratch

The coordinator repeats until all issues have accepted PRs.

### Phase 4b — Merge and unblock

When a PR is approved and ready:

1. **Merge** the PR: `gh pr merge <PR#> --squash`
2. **Update the dependency graph** — mark the issue as merged
3. **Unblock waiting agents** — recompute base branches and launch any newly unblocked issues
4. **Check for conflicts** — scan other open PRs for merge conflicts and send rebase directives
5. **Retarget PRs** — if a PR targeted an issue branch that has now merged, retarget it to the feature branch

### Phase 5 — Report

When all agents finish, the coordinator prints a summary table:

| Issue | PR | Base | Status | Turns | Cost |
|-------|-----|------|--------|-------|------|
| #10 | #15 | feature/login | ✅ merged | 42 | $8.30 |
| #11 | #16 | feature/login | ✅ merged | 38 | $7.10 |
| #12 | #17 | issue-10 → feature/login | ✅ merged | 55 | $11.20 |

## Dependency resolution

The coordinator uses `blocked-by: #N` annotations in issue descriptions to build the dependency graph. Base branch assignment follows these rules:

| Dependency state | Base branch | Action |
|-----------------|-------------|--------|
| No dependencies | Feature branch | Launch immediately |
| All deps merged | Feature branch | Launch immediately |
| Exactly one unmerged dep | That dep's issue branch (e.g., `issue-10`) | Launch immediately — inherits the dep's changes |
| Multiple unmerged deps | — | **Wait** until enough deps merge that at most one remains |

The "exactly one unmerged dep" rule is key: by branching from that dependency's issue branch, the agent gets the dependency's work for free without waiting for it to merge. When the dependency eventually merges into the feature branch, the dependent PR is retargeted.

After each merge, the coordinator recomputes the entire dependency graph — base branches and launch eligibility can change as the graph evolves.

## Merge ordering and conflict handling

The coordinator respects the dependency graph when merging: foundation issues merge first, then their dependents.

After each merge, the coordinator checks all remaining open PRs for conflicts:

```bash
gh pr list --base <feature-branch> --state open --json number,mergeable
```

For PRs with conflicts, it sends a rebase directive to the running agent:

```bash
cspace send issue-<N> "Rebase onto the latest <feature-branch> and resolve any conflicts."
```

If a PR's base branch was an issue branch that has now merged into the feature branch, the coordinator retargets the PR:

```bash
gh pr edit <PR#> --base <feature-branch>
```

## Status tracking

The coordinator maintains a status table throughout the run, updated as events occur:

| Issue | Deps | Base Branch | Container | Status |
|-------|------|-------------|-----------|--------|
| #10 | — | feature/login | issue-10 | ✅ merged |
| #11 | — | feature/login | issue-11 | 🔄 running |
| #12 | #10 | issue-10 | issue-12 | 🔄 running |
| #13 | #11, #12 | — | issue-13 | ⏳ blocked |

Status transitions:
- **⏳ blocked** → waiting for dependencies
- **🔄 running** → agent is active
- **✅ merged** → PR merged successfully
- **❌ failed** → agent failed, coordinator will iterate

## Communication model

The coordinator communicates with agents through two channels:

**Filesystem messages** (cross-container):
- Agent completion notifications land in `/logs/messages/_coordinator/inbox/`
- The coordinator watches this directory and receives completions as automatic user turns
- Directives are written to `/logs/messages/{instance}/inbox/`

**`cspace send` commands** (host-side dispatch):
- `cspace send issue-<N> "instructions"` — inject a directive into a running agent
- `cspace ask issue-<N>` — check for pending questions from an agent
- `cspace respond issue-<N> "answer"` — answer an agent's question

The coordinator never polls for status. Agent completions are delivered as structured user turns in its own conversation, including status, exit code, session ID, turn count, duration, cost, and a summary of the agent's final message.

## Coordinator rules

- **Max 4 parallel agents** — batch larger sets
- **Always `cspace warm` before launching** — never launch into unvalidated containers
- **Read full output files** on completion, not just the tail
- **Report each completion immediately** — don't batch notifications
- **Never fabricate PR URLs** — only report URLs found in the output
- **Always recompute base branches after each merge** — the dependency graph changes
