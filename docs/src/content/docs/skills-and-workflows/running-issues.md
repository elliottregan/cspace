---
title: Running Issues
description: Batch processing GitHub issues with autonomous cspace agents, including the "ready" label workflow.
sidebar:
  order: 2
---

import { Aside } from '@astrojs/starlight/components';

cspace can process GitHub issues in batch — hand a list of issue numbers (or all issues labeled `ready`) to the coordinator, and it spins up isolated containers, resolves dependencies, and ships PRs.

There are two entry points: `/run-issues` for an explicit list, and `/run-ready` for label-based automation.

## Run specific issues

The `/run-issues` skill takes a list of issue numbers and hands them to the coordinator.

### Step 1: Parse the issue list

Extract issue numbers from your message and confirm with a one-line summary per issue:

```bash
for n in 281 282 283; do
  gh issue view $n --json number,title,state \
    --jq '"#\(.number) \(.title) [\(.state)]"'
done
```

If any issues are already closed, they're surfaced so you can decide whether to skip them.

### Step 2: Feature branch decision

- **Default: no feature branch.** Each issue creates its own PR targeting `main`.
- **Feature branch** — only if you request it, the issues share a milestone, or they have inter-dependencies.

<Aside type="note">
Don't worry about scanning for dependencies yourself — the coordinator reads each issue body and builds a dependency graph as Phase 0 of its playbook.
</Aside>

### Step 3: Launch the coordinator

Write the instruction prompt to a file (never inline — see [prompt gotchas](/skills-and-workflows/delegating-container-agents/#always-write-prompts-to-a-file)) and launch:

```bash
cat > /tmp/coord-issues.txt <<'PROMPT'
Work on these GitHub issues, each independently targeting main:
#281, #282, #283

Each issue gets its own container and PR. Merge order does not matter.
PROMPT

cspace coordinate --prompt-file /tmp/coord-issues.txt
```

The instruction prompt should include:
- The list of issue numbers
- The base branch (`main`, or a feature branch)
- Anything not self-evident from the issues (e.g., "skip E2E for these", "merge into `feature/foo`", "stop if #281 fails")

**Example with a feature branch and sequencing hint:**

```bash
cat > /tmp/coord-issues.txt <<'PROMPT'
Work on these issues targeting feature/interview-improvements:
#535, #540

Follow the coordinator playbook. If you discover #540 needs
#535's changes, sequence them yourself.
PROMPT

cspace coordinate --prompt-file /tmp/coord-issues.txt
```

Run in the background with a 60-minute timeout.

### Step 4: Monitor and report

When the coordinator completes, read its output and review the final summary. If it failed, the coordinator is **resumable** — re-running with the same `--name` picks up where it left off.

## Run ready-labeled issues

The `/run-ready` skill automatically fetches all open issues with the `ready` label and hands them to the coordinator.

### Step 1: Fetch ready issues

The label name comes from your `.cspace.json` configuration (`agent.issue_label`, default: `"ready"`):

```bash
LABEL=$(jq -r '.agent.issue_label // "ready"' .cspace.json 2>/dev/null || echo "ready")

gh issue list --label "$LABEL" --state open \
  --json number,title,labels,milestone \
  --jq '.[] | "#\(.number) \(.title) [\(.labels | map(.name) | join(", "))]"'
```

If no issues are found, the workflow stops.

### Step 2: Feature branch decision

Same rules as `/run-issues` — default is no feature branch, each issue gets its own PR targeting `main`.

### Step 3: Launch the coordinator

```bash
cat > /tmp/coord-ready.txt <<'PROMPT'
Work on these ready-labeled issues, each independently targeting main:
#538, #537, #536, #519

Follow the coordinator playbook. Each gets its own container and PR.
Merge order does not matter.
PROMPT

cspace coordinate --prompt-file /tmp/coord-ready.txt
```

Run in the background with a 60-minute timeout.

### Step 4: Monitor and report

Same as `/run-issues` — read the coordinator output, review the summary. Resumable on failure.

### Step 5: Clean up labels

After all PRs are created, remove the `ready` label from processed issues:

```bash
gh issue edit 538 --remove-label "ready"
gh issue edit 537 --remove-label "ready"
```

## Configuring the issue label

The label used by `/run-ready` defaults to `"ready"` but can be changed in `.cspace.json`:

```json title=".cspace.json"
{
  "agent": {
    "issue_label": "auto"
  }
}
```

This changes the label that `/run-ready` queries for, so you can use whatever label convention fits your project.

## Comparison

| Feature | `/run-issues` | `/run-ready` |
|---------|---------------|--------------|
| Input | Explicit list of issue numbers | All open issues with configured label |
| Label required | No | Yes (`agent.issue_label`, default `"ready"`) |
| Label cleanup | No | Yes — offers to remove label after PR creation |
| Coordinator | Same coordinator playbook | Same coordinator playbook |
| Resumable | Yes | Yes |

<Aside type="tip">
Both workflows use the same coordinator under the hood. The only difference is how issues are selected — explicit list vs. label query.
</Aside>
