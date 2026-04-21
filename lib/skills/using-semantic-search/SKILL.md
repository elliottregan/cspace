---
name: using-semantic-search
description: >
  Use when you need to find where a concept lives in the codebase, what the
  history of work on a problem has been, which open decisions or findings
  bear on a question, or what prior issues and PRs discuss a bug. Available
  via MCP tools (search_code, search_context, search_issues, list_clusters)
  inside cspace containers and via the `cspace search` CLI from any shell.
  Reach for this BEFORE grep when you're asking a conceptual question like
  "where does X happen?" or "has anyone tackled Y before?" — grep matches
  tokens, semantic search matches ideas across different vocabulary.
---

# Using Semantic Search

Four search tools ship together. They answer different questions about the
same project. Pick the one that matches your question; combine them when the
answer spans multiple slices of truth.

## Quick picker

| You're asking... | Use |
|---|---|
| "Where does this concept live in the code right now?" | `search_code` |
| "Has this been worked on before? What was the arc?" | `cspace search commits` (no MCP tool yet) |
| "Are there open findings or prior decisions about this?" | `search_context` |
| "Has this come up in a GitHub issue or PR?" | `search_issues` |
| "What are the architectural zones of this codebase?" | `list_clusters` |

## The tools in detail

### `search_code(query, top_k=10, with_cluster=false)`

Natural-language search over source files. Returns ranked file chunks with
`path`, `line_start`, `line_end`, and a relevance `score`. Use when you need
to answer "where does X happen?" and don't know which files to open.

- Good queries: natural-language *concepts*, not keywords.
  `"routing from client to host"` > `"routing"`.
- Score interpretation: **>0.5 is strong**, 0.3–0.5 is "relevant but not
  load-bearing", **<0.3 is "no strong match"** — the concept may not exist
  in this codebase, which is itself useful signal.
- Set `with_cluster=true` when you want to know which architectural zone
  each hit belongs to (pairs with `list_clusters`).

### `search_context(query, top_k=10)`

Search over `.cspace/context/` planning artifacts: `direction.md`,
`principles.md`, `roadmap.md`, and any findings, decisions, or discoveries
recorded by agents. Use before proposing a change to check whether the
question has been decided already, or whether an open finding is relevant.

If the corpus is empty (a fresh project without `.cspace/context/`
populated), the tool silently returns zero hits. That's normal.

### `search_issues(query, top_k=10)`

Search over the GitHub issues and PRs for the current repo (open + closed,
with comments). The embedding covers titles, bodies, AND comment threads —
so prior *discussion* is searchable, not just issue titles. Use to find:

- Bugs that were filed previously with a different vocabulary than yours.
- PRs where a similar approach was attempted, accepted, or rejected.
- Design discussions scattered across issue comments.

If the cspace container doesn't have `GH_TOKEN` set, the corpus will be
empty — the tool degrades gracefully but won't surface any history.

### `list_clusters()`

Returns the architectural zones of the code corpus: cluster ID, size, and
top representative files. Read it once per conversation to understand the
shape of the project, then use the cluster IDs in `search_code(...,
with_cluster=true)` to place specific concerns on the map.

No input. Reads pre-computed cluster IDs — does not re-run the expensive
HDBSCAN pipeline.

## Composition patterns

### The architectural reconnaissance pass

When you're new to a codebase or an unfamiliar area:

1. `list_clusters()` — what are the zones?
2. `search_code("<your concern>", with_cluster=true)` — which zone(s) does
   your concern live in?
3. Read 2–3 files from the relevant cluster(s), not just the top hits.

### The "should I change this?" pass

Before proposing a non-trivial change:

1. `search_context("<concern>")` — has a principle or decision already
   weighed in?
2. `search_issues("<concern>")` — has this been discussed or attempted?
3. `search_code("<concern>")` — where does it currently live?

The order matters: prior decisions and discussions are cheaper to reckon
with than code you've already started editing.

### The "why does it look this way?" pass

When existing code surprises you:

1. `search_code("<surprising thing>")` — locate it, find neighbors.
2. `cspace search commits "<surprising thing>"` (CLI) — what was the arc
   that produced this shape? Which clusters of commits touch these files?

## Heuristics

- **If the top score is < 0.3, stop and reconsider the query.** The
  concept may not exist in this corpus. Don't force-rank a list of
  weak hits — it will misdirect the next step.
- **Dedupe by `path`, not by rank.** One file can appear multiple
  times when chunked; the results are already deduped, but when you
  follow-up-read, stick to unique paths.
- **Combine corpora for breadth.** Answer "where is auth?" in code,
  but "how did we end up with this auth model?" in commits + issues.
- **Re-query with synonyms if scores are weak.** Embeddings bridge
  vocabulary, but not infinitely. "Routing" and "service discovery"
  may both return hits for different nearby files.

## Pitfalls

- **Don't use semantic search when you already know the symbol.**
  If you're looking for a specific function name, grep is faster and
  more precise. Semantic search is for when you know the *idea* but
  not the *name*.
- **Don't over-index on cluster membership.** Clusters are a hint
  about architectural zones — they're computed from embeddings, not
  from a human architect's judgment. Use them as starting points,
  not as ground truth.
- **Don't paste raw query output as an answer.** The tool gives you
  file paths and scores. The answer to the user's question is
  whatever you *learn* by reading those files.

## Index freshness

Indexes auto-refresh on `post-commit`, `post-checkout`, and `post-merge`
lefthook hooks (if lefthook is installed for the project). If you suspect
the index is stale — e.g. you just pulled a large change and the tool
surfaces gone files — run:

```
cspace search code index
cspace search commits index
cspace search issues index   # if gh is authenticated
cspace search context index
```

Or `cspace search init` to bootstrap all of them at once in a new project.
