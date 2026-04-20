# Semantic code search with corpus abstraction

**Date:** 2026-04-20
**Status:** Approved

## Problem

The existing `cspace search` indexes commit history and surfaces "where has this been worked on before?" This is useful historical signal but doesn't tell an agent or developer **where the relevant load-bearing code lives right now**. Surface-level keyword grep misses architectural chains that span languages and file types — for example, the client-reaches-server routing chain in cspace touches Go (`InjectHosts`), YAML (Traefik labels), shell (`post-setup.sh` env wiring), and markdown (design specs). A bug that appears as "host can't reach server" needs all of those to be visible before a fix is proposed; otherwise the fix is a surface patch that misses the architecture.

The goal is a semantic search tool over the **current state of the codebase** that:

- Finds conceptually related files across languages and formats (not just matching identifiers).
- Surfaces architectural **neighborhoods** (clusters), not just isolated hits, so fixes can be informed by the full subsystem.
- Is primarily agent-facing (MCP tool) but has a CLI for human verification during build.
- Stays fresh automatically as the code evolves.

## Design

### Corpus abstraction

The existing search package hardcodes "commit" as the only unit of indexing. We formalize a `Corpus` primitive so the same embedding/qdrant/reduce/cluster pipeline can index multiple content types:

```
Corpus interface:
  ID()              → "commits" | "code" | "docs"(future)
  Collection(proj)  → Qdrant collection name (scoped per project + corpus)
  Enumerate()       → emit Records (file-level OR chunk-level)
  EmbedText(rec)    → text sent to llama-server embedding adapter
  DisplayMeta(rec)  → {path, lineStart, lineEnd, kind, hash, mtime}
```

Two implementations ship in this spec:

- **CommitCorpus** — today's logic, refactored onto the interface. No behavior change visible to users.
- **CodeCorpus** — new. Walks `git ls-files`, filters, chunks oversized files, emits records.

Future corpora (docs, `.cspace/context/findings`) are additional `Corpus` implementations + config entries; they are explicitly **out of scope for this spec** but the architecture anticipates them.

### Content scope for CodeCorpus

- Base set: `git ls-files` (respects `.gitignore`).
- Auto-skip: binary detection via null-byte check on the first 1 KB; size cap at 200 KB; files marked binary in `.gitattributes`.
- Exclude globs (configurable): `vendor/**`, `internal/assets/embedded/**`, `docs/superpowers/specs/**`, `*.lock`, `*.sum`, `package-lock.json`.

The exclusion of `docs/superpowers/specs/**` is deliberate for this POC: design specs are often written at a point in time and not maintained as the code evolves, so including them risks agents reasoning about phantom architecture. Docs can later be indexed as a separate corpus, retrieved explicitly as reference material, without mixing into `code` results.

### Granularity

- Files ≤ 12 000 chars (≈ 8 K tokens, the Jina v5 embedding ceiling with headroom) → one embedding per file.
- Files > 12 000 chars → split into ~8 000-char chunks with 200-char overlap. Each chunk becomes its own Qdrant point with a line range in its payload.
- Qdrant payload always carries: `{path, line_start, line_end, kind: "file" | "chunk", content_hash, mtime, cluster_id}`.

For clustering, one embedding per file is used (the chunk covering line 1 for chunked files). This keeps cluster grain at the "file = architectural zone member" level rather than fragmenting zones into chunk-sized pieces.

`cluster_id` is written back into the Qdrant payload during `cspace-search clusters`: the command re-clusters the corpus and upserts the new `cluster_id` onto every point (all chunks of a file share their file's cluster). Queries with `with_cluster=true` then read `cluster_id` directly from the payload with no additional compute. The field is `null` on points indexed before the first cluster run.

Collection naming: `{corpus_id}-{project_hash}`, where `project_hash` is the first 8 hex chars of the sha256 of the absolute project root. Matches the pattern used by the existing commit indexer (`git-search-<hash>-clustering`) while giving each corpus its own isolated collection.

### Freshness

Two triggers, one code path:

- **Manual:** `cspace-search index --corpus=code` (or `cspace search code index`). Used during initial provision and by the hook.
- **Automatic:** `lefthook.yml` entries for `post-commit`, `post-checkout`, `post-merge` invoke the same command in the background (`&` + detached), with output logged to `.cspace/search-index.log`.

A simple lock file (`.cspace/search-index.lock`) prevents overlapping runs. The indexer skips unchanged files by comparing the on-disk sha256 against the stored `content_hash` in Qdrant; only new-or-changed files are re-embedded. Orphan points (path not in current `git ls-files`) are deleted.

The freshness model assumes the consumer of the index is a **different session** from the one making edits. Cspace sessions share state through commits (push/pull via GitHub), not through the working tree. Indexing on commit boundaries is therefore sufficient — there is no need to track working-tree edits made in the same session that runs the query.

### Packaging

Search stays in the cspace repo and module for now, with a layout that makes future extraction a `git mv` + module-rename, not a refactor:

```
cspace/ (existing module)
  cmd/cspace/                        existing CLI
  cmd/cspace-search/                 NEW — standalone search CLI binary
  cmd/cspace-search-mcp/             NEW — MCP server binary (stdio)
  internal/cli/search.go             thin shim: in-process import of search/
  search/                            NEW — top-level package (NOT under internal/)
    corpus/  embed/  qdrant/
    reduce/  cluster/  index/
    config/default-corpora.yaml      default corpora for cspace itself
  lib/templates/docker-compose.search.yml
```

Two load-bearing rules:

1. `search/` lives outside `internal/` so when we extract, import paths inside the package are stable. Only the module prefix changes.
2. Nothing under `search/` imports from `cspace/internal/...` or `cspace/cmd/...`. Enforced by a CI grep check for now; promote to a proper linter if it fires often.

Cspace (`internal/cli/search.go`) may import `search/` — the dependency arrow runs only one way.

Two binaries are produced by `make build`:

- `cspace-search` — standalone CLI. Can be used in any repo with a `search.yaml`, no cspace required. This is the dogfooding surface for using the tool in other projects before extraction.
- `cspace-search-mcp` — MCP server, runs as a compose sidecar, speaks stdio.

### Compose topology

A new compose file, `lib/templates/docker-compose.search.yml`, is imported by `docker-compose.core.yml`. It adds the `cspace-search-mcp` service. The existing sidecars (`llama-server`, `qdrant`, `reduce-api`, `hdbscan-api`) are already in `docker-compose.core.yml`; they remain there since the commit search uses them too.

### CLI surface

Standalone binary (`cspace-search`):

```
cspace-search index [--corpus=code] [--quiet]
cspace-search query <query> [--corpus=code] [--top=10] [--json] [--with-cluster]
cspace-search clusters [--corpus=code] [--coords-out=FILE]
```

In-repo wrapper (`cspace search`), in-process import of `search/`:

```
cspace search code "<query>"          → query --corpus=code
cspace search code index              → index --corpus=code
cspace search code clusters           → clusters --corpus=code
cspace search commits "<query>"       → existing commit search, refactored onto Corpus
cspace search "<query>"               → alias for commits (back-compat)
```

### Result shape

Single hit:

```json
{
  "path": "internal/docker/docker.go",
  "line_start": 142,
  "line_end": 198,
  "score": 0.78,
  "kind": "file",
  "content_hash": "sha256:abc...",
  "preview": "first ~300 chars of the chunk",
  "cluster_id": 5
}
```

`cluster_id` is included only when `--with-cluster` (or MCP parameter) is set. `kind` is `"file"` when the whole file was embedded as one unit, `"chunk"` when it is a piece of a larger file.

Response envelope:

```json
{
  "query": "client reaches server",
  "corpus": "code",
  "results": [...],
  "indexed_at": "2026-04-20T17:00:00Z"
}
```

`indexed_at` is the most recent indexing run for this corpus. Agents can notice staleness at a glance.

### MCP tool surface

The `cspace-search-mcp` binary exposes two tools:

**`search_code`**

```
query: string          — natural-language concept
top_k: int (default 10, max 50)
with_cluster: bool (default false)
```

Returns the response envelope above.

**`list_clusters`**

Returns the full cluster map for the code corpus:

```json
{
  "corpus": "code",
  "clusters": [
    { "cluster_id": 5, "size": 9, "top_paths": ["...", "..."] },
    ...
  ],
  "indexed_at": "..."
}
```

Agents call this once to understand the architectural layout, then `search_code` with `with_cluster=true` to place specific concerns on the map.

No indexing or refresh tool is exposed through MCP — indexing is a host-side concern driven by hooks + the manual CLI. Agents query a pre-built index; they do not trigger rebuilds.

### Error handling

| Condition | Behavior |
|---|---|
| Any sidecar unreachable | Error naming the service and suggesting `docker compose up`. Indexer logs and exits non-fatal; next hook run retries. Query path returns structured error. |
| Qdrant collection missing | Query returns `{"error": "index_empty", "hint": "run `cspace search code index`"}`. MCP maps to tool error. |
| File changed mid-index | Indexer re-stats at upsert time; if the hash differs from what was embedded, the file is skipped and re-picked up on the next run. |
| Embedding fails (oversized or unreadable bytes) | Log to `.cspace/search-index.log`, skip the file, continue. |
| Orphan cleanup | On each index run, Qdrant points whose `path` is absent from `git ls-files` are deleted. |
| Concurrent index runs | Lock file `.cspace/search-index.lock`; second run exits 0 silently. |
| Degenerate query (empty / all-zero embedding) | Return empty results with a `warning` field in the envelope. No crash. |

### Testing

Unit:

- Chunker line-range accuracy; overlap preserves concepts across boundaries; line numbers are recoverable from chunk offsets.
- CodeCorpus filter: binary files excluded, size cap honored, explicit-exclude globs honored, `.gitignore`-respected files excluded.
- Content-hash change detection: modifying one byte triggers re-embedding of that file only.

Integration (requires the search compose stack):

- Seed fixture repo with three obvious subsystems; index; query each concept; assert top hits land in the expected subsystem.
- Cross-vocabulary test: fixture with YAML and Go talking about the same concern in different words; query in one vocabulary, assert hits in the other.
- Cluster test: seed 3 obvious subsystems; assert HDBSCAN finds ≥3 clusters containing the expected files.
- Freshness test: index → modify → re-index → query returns new content, not old.

MCP contract:

- Launch `cspace-search-mcp` in a test harness; send `search_code` and `list_clusters` calls; assert JSON shape matches this spec.
- Error paths: missing index, unreachable sidecars, oversized `top_k`.

CI:

- Unit tests run on every PR (no sidecars needed).
- Integration tests run in a dedicated job that spins up the search compose stack.

## Scope explicitly deferred

The following are intentionally out of scope for this POC and will be revisited after beta + dogfooding in another repo:

- Docs corpus. The config slot is present but disabled; staleness risk must be mitigated separately before enabling.
- `.cspace/context/findings/` corpus.
- Extraction to a separate repo / Go module. Layout is extraction-ready but we do not extract yet.
- Plugin packaging of the search stack.
- Query-side caching, re-ranking, hybrid keyword/semantic scoring.
- Web UI for the cluster plot beyond the existing `scripts/plot-clusters.py`.
- Chain expansion via static analysis (following imports, Traefik label refs, env-var consumers across languages). Evaluated in Section "Retrieval strategy"; deferred pending evidence that broad semantic + clustering leave architectural gaps.

## Retrieval strategy decision record

Three strategies were considered for ensuring architectural completeness:

- **Broad semantic retrieval** — trust the embedding model to bridge vocabulary gaps. Simple, cheap.
- **Cluster-anchored retrieval** — pre-cluster the corpus into architectural zones; queries route to zones and return zone members + top chunks.
- **Chain expansion** — semantic seed → mixed-language static analysis to follow imports, Traefik label refs, env-var consumers.

This spec implements **broad semantic + cluster-anchored** together. The combination reuses all existing pipeline components and gives agents both point hits (for targeted reads) and a coarser "architectural zone" frame (for completeness). Chain expansion is deferred; it is a substantial additional project that is only worth the cost if real miss patterns emerge from usage.

## Files changed

- `search/` (new package tree)
- `cmd/cspace-search/` (new binary)
- `cmd/cspace-search-mcp/` (new binary)
- `internal/cli/search.go` (refactored into a shim; existing commit logic moves into `search/corpus/commits.go`)
- `internal/search/` (deleted; contents redistributed under `search/`)
- `lib/templates/docker-compose.search.yml` (new)
- `lib/templates/docker-compose.core.yml` (include the new compose file)
- `lefthook.yml` (add `post-commit`, `post-checkout`, `post-merge` entries)
- `Makefile` (build the two new binaries)
- `search/config/default-corpora.yaml` (new, with cspace defaults)
- Tests per the Testing section.

## Testing plan (end-to-end)

1. `make build` produces `cspace-go`, `cspace-search`, `cspace-search-mcp`.
2. `cspace up venus --no-claude` starts the search stack alongside the devcontainer.
3. From host: `cspace search code "routing from client to host"` returns hits in `internal/docker/docker.go`, `lib/templates/docker-compose.core.yml`, `lib/templates/devcontainer/*/docker-compose.yml`, and at least one shell file touching `VITE_CONVEX_URL`.
4. From inside a cspace container: `cspace-search query --corpus=code "routing from client to host"` returns the same result set (same index, same URL pattern the hosts-injection design already enables).
5. From an agent: MCP `search_code` call returns the same JSON envelope; `list_clusters` returns a map where the routing files cluster together.
6. Edit a file, commit; the `post-commit` hook re-indexes in the background; next query returns updated content.
