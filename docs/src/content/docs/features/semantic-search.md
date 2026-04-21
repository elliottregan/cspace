---
title: Semantic Search
description: Search commits, code, project context, and GitHub issues with natural-language queries. Works from the CLI and from agents via MCP.
sidebar:
  order: 2
---

import { Aside } from '@astrojs/starlight/components';

**Semantic search** lets you (and the agents running inside cspace containers) ask natural-language questions about a project and get back the files, commits, decisions, or issues that best answer them. It complements grep: grep matches tokens, semantic search matches *ideas*, including across files that share almost no vocabulary.

Four corpora ship together, all driven by the same embedding + clustering pipeline:

| Corpus | Answers the question... |
|---|---|
| `code` | "Where does this concept live in the codebase right now?" |
| `commits` | "Has this been worked on before? What was the arc of attempts?" |
| `context` | "Is there an open finding, prior decision, or stated principle on this?" |
| `issues` | "Has this come up in a GitHub issue or PR?" |

Agents running inside cspace containers get MCP tools (`search_code`, `search_context`, `search_issues`, `list_clusters`) auto-registered on provisioning. You get the same functionality on the host via `cspace search`.

## How to use it

From inside a cspace instance (sidecars reachable by default) or from the host with the sidecars reachable:

```sh
# First-time setup for a fresh project: writes search.yaml, installs
# lefthook hooks if available, runs initial indexes.
cspace search init

# Ask a natural-language question of the codebase
cspace search code "routing from client to host"

# Top hits returned with score, file path, and line range:
#   0.398  lib/templates/proxy/Corefile:1-5  (file)
#   0.392  lib/templates/docker-compose.shared.yml:1-50  (file)
#   0.390  lib/templates/proxy/docker-compose.yml:1-31  (file)
#   ...

# Discover thematic clusters across the corpus
cspace search code clusters

# Same for commits, GitHub issues, or planning context
cspace search commits "authentication refactor"
cspace search issues "performance regression"
cspace search context "have we decided about X?"
```

Scores over 0.5 are strong matches; 0.3–0.5 is "relevant but not load-bearing"; under 0.3 is "no strong match in this corpus" — itself useful signal, since it tells you the concept may not live here.

## Index freshness

Indexes auto-refresh on `post-commit`, `post-checkout`, and `post-merge` [lefthook](https://github.com/evilmartians/lefthook) hooks when lefthook is installed. `cspace search init` installs the hooks for you.

On provisioning, every new cspace instance kicks off `cspace search init --quiet` in the background after MCP registration, so you start with a warm index on fresh containers.

To refresh all running instances of a project after pulling a cspace update:

```sh
cspace rebuild --reindex
```

This rebuilds the container image and then dispatches a background re-index into every running instance of the current project. Output goes to `.cspace/search-index.log`.

## When to reach for it

Good fits:

- **Architectural exploration.** Before making a non-trivial change, ask "where does X live?" and read the cluster of files the query surfaces — not just the top one. The cluster IDs tell you which architectural zone you're about to touch.
- **Bug discovery.** A symptom described in user-language (e.g. "client can't reach server") often maps to a chain of commits (Traefik → hosts injection → env wiring) even though no single token shows up in all of them. Semantic search finds the chain.
- **Decision archaeology.** "Has this been decided before?" reaches both the `context` corpus (explicit decisions) and the `commits` corpus (implicit ones in the form of reverts and rewrites).
- **Agent self-orientation.** When a Claude Code agent is dispatched into an unfamiliar codebase via cspace, the four MCP tools let it build a map of the project before editing anything.

Skip it when:

- **You already know the symbol.** If you're searching for a function name, grep is faster and more precise. Semantic search earns its cost when you know the *idea* but not the *name*.
- **The question is historical and narrow.** For "who last touched this line?" use `git blame`, not `cspace search commits`.
- **You need exact regex matching.** Semantic embeddings bridge vocabulary; they don't enforce patterns.

## MCP tools for agents

Agents in a cspace container call these tools by name without knowing about the binaries behind them:

- **`search_code(query, top_k=10, with_cluster=false)`** — ranked code hits with path + line range + score.
- **`search_context(query, top_k=10)`** — ranked hits from `.cspace/context/` artifacts (direction, principles, roadmap, findings, decisions, discoveries).
- **`search_issues(query, top_k=10)`** — ranked GitHub issue/PR hits (titles + bodies + comments).
- **`list_clusters()`** — the architectural zones HDBSCAN found over the code corpus, with cluster IDs and representative files.

See the shipped `using-semantic-search` skill for composition patterns (reconnaissance pass, "should I change this?" pass, "why does it look this way?" pass).

## What you configure

A `search.yaml` at the project root overrides the embedded defaults. Common shapes:

```yaml
sidecars:
  # Point at non-default sidecar hostnames
  qdrant_url: "http://qdrant:6333"

corpora:
  code:
    # Add project-specific exclude globs on top of vendor/**, *.lock, etc.
    excludes:
      - "generated/**"
      - "*.pb.go"
  commits:
    limit: 500
  context:
    enabled: true
  issues:
    enabled: true
    limit: 500
```

`cspace search init` writes a commented template if none exists.

## Under the hood

- **Embedding model:** Jina v5 nano (`jina-embeddings-v5-text-nano`), 768 dims, ~8 K token context. Served by a `llama.cpp` sidecar.
- **Vector store:** Qdrant, one collection per `(corpus, project)` pair.
- **Dimensionality reduction:** PaCMAP, via a small `reduce-api` sidecar.
- **Clustering:** HDBSCAN, via a minimal `hdbscan-api` sidecar.
- **Freshness:** content-hash skip on unchanged files, `O_EXCL` lock file to prevent overlapping runs, orphan cleanup on every index pass.

See [`docs/superpowers/specs/2026-04-20-code-search-design.md`](https://github.com/elliottregan/cspace/blob/main/docs/superpowers/specs/2026-04-20-code-search-design.md) for the original design.
