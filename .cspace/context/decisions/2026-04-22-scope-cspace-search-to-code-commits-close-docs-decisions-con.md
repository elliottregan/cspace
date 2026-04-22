---
title: Scope cspace-search to code + commits; close docs/decisions/context corpus expansion work
date: 2026-04-22
kind: decision
---

## Context
During a design brainstorm we'd opened four issues expanding semantic search with curated text corpora: #54 (split code into code/docs/content), #55 (decisions sub-corpus for ADRs + specs + findings), #56 (cross-corpus "search why" retrieval), and #57 (user-configurable context paths). The motivating hypothesis was that curated rationale deserves its own index so "why did we pick X" queries don't compete against ambient README text.

On reflection: the real bottleneck isn't retrieval. Text documents are small enough that an agent can read them directly when pointed at them; findings and ADRs are never so numerous that an advisor can't scan the list. The actual failure mode is discipline — coordinators and advisors forgetting to consult curated resources at the right moments — which no amount of better embedding fixes. Chunking + embedding + qdrant-indexing a handful of markdown files pays real cost (re-index on every change, qdrant storage, log noise) for negligible retrieval benefit when the corpus is O(dozens) not O(thousands).

We also re-examined the default corpus set: today `code`, `commits`, `context`, and `issues` all index by default. GitHub issues indexing in particular needs `GH_TOKEN`, hits the API on every bootstrap, and many projects don't use issues as a primary signal. Context (curated `.cspace/context/`) is exactly the low-value case above — agents can read those files directly.

## Alternatives
1. **Ship #54/#55/#57 as designed** — build a docs corpus, a decisions corpus, and `extra_paths` config. Rejected: solves a retrieval problem that isn't blocking us; real blocker is agent consultation discipline.

2. **Keep the curated corpora but make them opt-in** — leave the code for #54/#55 in place, ship behind disabled-by-default flags. Rejected: still carries maintenance cost (indexing logic, Qdrant collection schemas, MCP tool surface) for a feature that may never prove out. Easier to re-open the closed issues if a real need surfaces.

3. **Ship only `extra_paths` on the existing context corpus** (the #57 cheap-win path). Rejected: we concluded the context corpus itself isn't pulling weight, so extending its reach doesn't fix the underlying problem.

4. **Defer the decision, leave issues open** — mark them "needs data" and revisit in N weeks. Rejected: open issues accumulate inertia; an explicit close with a clear reopen trigger (#56 usage data) is cleaner than ambiguous limbo.

## Decision
Scope semantic search to the two corpora that earn their keep: `code` (what's here now) and `commits` (how it got here). Everything else becomes opt-in, with new defaults:

- `code`: enabled
- `commits`: enabled
- `context`: **disabled** (change from today's default)
- `issues`: **disabled** (change from today's default)

Closed #54, #55, and #57 with explanations pointing back to this decision. Kept #56 ("search why" composite retrieval) open as the mechanism to revisit curated-rationale retrieval once we have real usage data showing it's needed. Opened #60 to implement the configurable-corpus + new-defaults change.

## Consequences
- New cspace projects only index code + commits out of the box. Less log noise, no GH_TOKEN churn, smaller qdrant volume.
- Existing projects keep their existing `context` / `issues` collections but stop auto-refreshing them unless opted back in via `search.yaml` (per #60's migration plan).
- If "agents should read curated docs at moment X" keeps being a problem, the fix is in the advisor/coordinator playbooks (`lib/agents/*.md`) and the findings lifecycle, not in search infrastructure.
- `search why` (#56) stays on the table. If we implement it later and it turns out cross-corpus retrieval needs dedicated curated indexes to work well, we'll re-open #54/#55 with real usage data as justification rather than speculative design.
- Precedent set: new corpora ship disabled by default. #53 (sessions corpus) would inherit this rule when it lands.
- Fewer moving pieces means fewer places where "did the index get rebuilt?" becomes a debugging question — which matches the lesson from the qdrant-storage volume collision we just fixed.
