# Semantic Code Search Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add semantic search over the current codebase, alongside the existing commit search, using a corpus abstraction that lets both share the same embedding / Qdrant / clustering pipeline.

**Architecture:** A new `search/` top-level package introduces a `Corpus` interface with `CommitCorpus` (refactored from today's logic) and `CodeCorpus` (new) implementations. Two new binaries — `cspace-search` (standalone CLI) and `cspace-search-mcp` (MCP server) — expose the library to humans and agents. The existing `cspace search` command becomes a thin in-process wrapper. Freshness is maintained by a lefthook post-commit/checkout/merge hook that re-indexes only changed files.

**Tech Stack:** Go 1.22+, `github.com/modelcontextprotocol/go-sdk/mcp` for the MCP server, Qdrant (REST), llama.cpp embedding sidecar (Jina v5 nano, 768-dim), reduce-api (PaCMAP), hdbscan-api. YAML config via `gopkg.in/yaml.v3`. Tests via stdlib `testing`.

**Reference spec:** `docs/superpowers/specs/2026-04-20-code-search-design.md`.

---

## File map

**New files:**
```
search/
  corpus/corpus.go         Corpus interface, Record type, payload helpers
  corpus/commits.go        CommitCorpus (refactored from internal/search/git.go)
  corpus/code.go           CodeCorpus: git ls-files walk, chunker, hashing
  corpus/code_filter.go    binary/size/exclude filter
  corpus/code_chunker.go   line-range chunker with overlap
  embed/embed.go           llama-server client (moved from internal/search/embed.go)
  qdrant/qdrant.go         Qdrant client (moved + payload schema widened)
  reduce/reduce.go         reduce-api client (moved from internal/search/cluster.go)
  cluster/cluster.go       hdbscan-api client + cluster pipeline
  index/index.go           generic indexer: takes Corpus, does embed+upsert+orphan cleanup
  query/query.go           generic query path: embed query, ANN, dedupe, envelope
  config/config.go         search.yaml parser + defaults
  config/default.yaml      embedded default corpora config
  mcp/server.go            MCP server with search_code + list_clusters tools
cmd/cspace-search/main.go         standalone CLI entry (cobra, same pattern as cspace)
cmd/cspace-search-mcp/main.go     MCP server entry (stdio transport)
lib/templates/docker-compose.search.yml
```

**Modified files:**
```
internal/cli/search.go          thin shim: in-process calls into search/query + search/index
internal/cli/search_test.go     characterization test for `cspace search commits` back-compat
internal/search/                DELETED (contents moved under search/)
lib/templates/docker-compose.core.yml    include: docker-compose.search.yml
lefthook.yml                    add post-commit, post-checkout, post-merge
Makefile                        build cspace-search and cspace-search-mcp
.cspace/.gitignore              search-index.log, search-index.lock
```

---

## Qdrant payload schema

Today: `map[string]string`. This plan widens it to `map[string]any` to support structured fields:

```go
type Payload map[string]any

// Canonical fields written by indexer:
//   path          string      file path relative to project root (code corpus)
//                             or commit hash (commits corpus)
//   kind          string      "file" | "chunk" | "commit"
//   line_start    int         1-based, inclusive (code corpus; 0 for commits)
//   line_end      int         1-based, inclusive (code corpus; 0 for commits)
//   content_hash  string      hex sha256 of source bytes
//   mtime         string      RFC3339 timestamp
//   cluster_id    int         -1 if not yet clustered or noise
//   // commits-specific:
//   hash          string
//   date          string
//   subject       string
```

---

## Phase 1 — Scaffold and refactor commits onto Corpus interface

Goal of this phase: introduce the new package tree; move existing commit-search logic onto it with zero behavior change. At the end of Phase 1, `cspace search "<query>"` still works exactly as it does today, just with code reorganized under `search/`.

### Task 1.1: Scaffold the search/ package tree

**Files:**
- Create: `search/corpus/corpus.go`, `search/embed/embed.go`, `search/qdrant/qdrant.go`, `search/reduce/reduce.go`, `search/cluster/cluster.go`, `search/index/index.go`, `search/query/query.go`, `search/config/config.go`

- [ ] **Step 1: Create empty package files**

Create each file with just the package declaration. Example for `search/corpus/corpus.go`:

```go
package corpus
```

Repeat for: `embed`, `qdrant`, `reduce`, `cluster`, `index`, `query`, `config`.

- [ ] **Step 2: Add a one-way-dependency CI guard**

Create `scripts/check-search-imports.sh`:

```bash
#!/usr/bin/env bash
# Verify nothing under search/ imports from cspace internals.
set -euo pipefail
if grep -rn "elliottregan/cspace/internal\|elliottregan/cspace/cmd" search/ 2>/dev/null; then
  echo "ERROR: search/ must not import from cspace internals." >&2
  exit 1
fi
echo "search/ dependency rule OK"
```

```bash
chmod +x scripts/check-search-imports.sh
```

- [ ] **Step 3: Run it to verify nothing fails**

```bash
bash scripts/check-search-imports.sh
```

Expected output: `search/ dependency rule OK`

- [ ] **Step 4: Commit**

```bash
git add search/ scripts/check-search-imports.sh
git commit -m "Scaffold search/ package tree with one-way dependency guard"
```

---

### Task 1.2: Define Corpus interface and Record type

**Files:**
- Modify: `search/corpus/corpus.go`
- Test: `search/corpus/corpus_test.go`

- [ ] **Step 1: Write the test for the Record type contract**

```go
package corpus

import "testing"

func TestRecord_IDIsStableForSameInputs(t *testing.T) {
	r1 := Record{Path: "foo.go", LineStart: 10, LineEnd: 20, Kind: "chunk"}
	r2 := Record{Path: "foo.go", LineStart: 10, LineEnd: 20, Kind: "chunk"}
	if r1.ID() != r2.ID() {
		t.Fatalf("Record.ID should be deterministic: %d vs %d", r1.ID(), r2.ID())
	}
}

func TestRecord_IDDiffersOnDifferentKindOrRange(t *testing.T) {
	base := Record{Path: "foo.go", LineStart: 10, LineEnd: 20, Kind: "chunk"}
	cases := []Record{
		{Path: "bar.go", LineStart: 10, LineEnd: 20, Kind: "chunk"},
		{Path: "foo.go", LineStart: 1, LineEnd: 20, Kind: "chunk"},
		{Path: "foo.go", LineStart: 10, LineEnd: 21, Kind: "chunk"},
		{Path: "foo.go", LineStart: 10, LineEnd: 20, Kind: "file"},
	}
	for i, c := range cases {
		if c.ID() == base.ID() {
			t.Errorf("case %d: IDs collided: %d", i, c.ID())
		}
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./search/corpus/ -run TestRecord
```

Expected: FAIL — `Record` undefined.

- [ ] **Step 3: Implement Record and Corpus**

Replace `search/corpus/corpus.go`:

```go
// Package corpus defines the abstraction over indexable content types.
// A Corpus enumerates Records; the indexer embeds them and writes them
// to Qdrant. The same pipeline serves commits, code, and future corpora.
package corpus

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// Record is one unit of content to be indexed. A Record may represent a whole
// file, a chunk of a file, or a commit; the Kind field disambiguates.
type Record struct {
	// Path is the primary identifier: relative file path (code), commit hash
	// (commits), or similar.
	Path string

	// LineStart and LineEnd are 1-based inclusive line ranges for chunked
	// code records. Zero for whole-file or commit records.
	LineStart int
	LineEnd   int

	// Kind is "file", "chunk", or "commit".
	Kind string

	// ContentHash is the hex sha256 of the source bytes, used for
	// change detection.
	ContentHash string

	// Extra carries corpus-specific metadata that should land in the Qdrant
	// payload (e.g., commit Subject, Date).
	Extra map[string]any

	// EmbedText is the text to send to the embedding model. Populated by the
	// corpus at enumeration time so the indexer does not need to know the
	// corpus-specific embedding format.
	EmbedText string
}

// ID returns a deterministic 64-bit Qdrant point ID for this Record.
func (r Record) ID() uint64 {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%d\x00%d", r.Kind, r.Path, r.LineStart, r.LineEnd)
	sum := h.Sum(nil)
	return binary.BigEndian.Uint64(sum[:8])
}

// Corpus is the interface an indexable content type implements.
type Corpus interface {
	// ID is the stable corpus identifier, e.g. "commits", "code".
	ID() string

	// Collection returns the Qdrant collection name for this corpus + project.
	Collection(projectRoot string) string

	// Enumerate emits Records, one per unit of content. The channel is closed
	// when enumeration completes. Errors are reported via the errs channel
	// but do not halt enumeration of unaffected records.
	Enumerate(projectRoot string) (<-chan Record, <-chan error)
}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
go test ./search/corpus/ -run TestRecord -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add search/corpus/corpus.go search/corpus/corpus_test.go
git commit -m "Add Corpus interface and Record type"
```

---

### Task 1.3: Move embed, reduce, cluster clients; widen Qdrant payload

The existing clients in `internal/search/embed.go`, `internal/search/qdrant.go`, and `internal/search/cluster.go` are already generic except for the payload type and collection name. Move them under `search/`, widen the payload from `map[string]string` to `map[string]any`, and decouple the collection-name helper.

**Files:**
- Delete: `internal/search/embed.go`, `internal/search/qdrant.go`, `internal/search/cluster.go`
- Create: `search/embed/embed.go`, `search/qdrant/qdrant.go`, `search/reduce/reduce.go`, `search/cluster/cluster.go`
- Modify: any callers in `internal/cli/` that import the old paths

- [ ] **Step 1: Move embed.go verbatim**

```bash
git mv internal/search/embed.go search/embed/embed.go
```

Then edit the package declaration: change `package search` to `package embed`. No other changes.

- [ ] **Step 2: Move qdrant.go and widen the payload type**

```bash
git mv internal/search/qdrant.go search/qdrant/qdrant.go
```

In `search/qdrant/qdrant.go`:
- Change `package search` to `package qdrant`.
- Change `QdrantPoint.Payload` from `map[string]string` to `map[string]any`.
- Rename the public helper `CollectionName(repoPath string)` to `ProjectHash(repoPath string) string` returning only the hex hash (first 8 chars). Corpus implementations will use `{corpusID}-{projectHash}` to build collection names.

```go
// ProjectHash returns a stable 8-char hex hash for a project root,
// used as a component in Qdrant collection names.
func ProjectHash(repoPath string) string {
	abs, _ := filepath.Abs(repoPath)
	h := sha256.Sum256([]byte(abs))
	return fmt.Sprintf("%x", h[:4])
}
```

- [ ] **Step 3: Split cluster.go into reduce + cluster**

The existing `internal/search/cluster.go` has both the PaCMAP client (`ReduceTo2D`) and the HDBSCAN client. Split them:

```bash
git mv internal/search/cluster.go search/cluster/cluster.go
```

Then extract the `ReduceTo2D` + `ReduceRequest`/`ReduceResponse` types into a new file `search/reduce/reduce.go` (package `reduce`). Keep the HDBSCAN pieces in `search/cluster/cluster.go` (package `cluster`).

- [ ] **Step 4: Update imports in any caller**

```bash
grep -rln "elliottregan/cspace/internal/search" internal/ cmd/
```

For each match, rewrite imports:
- `elliottregan/cspace/internal/search` → the appropriate subpackage under `elliottregan/cspace/search/`.
- Calls to `search.CollectionName(root)` → construct as `"git-search-" + qdrant.ProjectHash(root)` to preserve the existing collection name.

- [ ] **Step 5: Build and run existing tests**

```bash
go build ./...
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Verify CI guard still passes**

```bash
bash scripts/check-search-imports.sh
```

Expected: OK.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "Move embed/qdrant/reduce/cluster clients under search/ with widened payload"
```

---

### Task 1.4: Extract CommitCorpus

**Files:**
- Move: `internal/search/git.go` → `search/corpus/commits.go` (with `package corpus`)
- Create: `search/corpus/commits_test.go`
- Delete when empty: `internal/search/` directory

- [ ] **Step 1: Write a characterization test first**

```go
package corpus

import (
	"strings"
	"testing"
)

// Test that CommitCorpus.Enumerate on the cspace repo emits at least one
// known commit with the expected subject. Uses the current working tree.
func TestCommitCorpus_EnumerateIncludesKnownCommit(t *testing.T) {
	cc := &CommitCorpus{Limit: 50}
	records, errs := cc.Enumerate(".")

	// Drain errors in the background.
	go func() {
		for range errs {
		}
	}()

	found := false
	for rec := range records {
		if rec.Kind != "commit" {
			t.Errorf("expected Kind=commit, got %q", rec.Kind)
		}
		if strings.Contains(rec.EmbedText, "semantic git search") {
			found = true
		}
	}
	if !found {
		t.Error("expected to find a commit mentioning 'semantic git search' in the recent history")
	}
}

func TestCommitCorpus_CollectionName(t *testing.T) {
	cc := &CommitCorpus{}
	got := cc.Collection(".")
	if !strings.HasPrefix(got, "commits-") {
		t.Errorf("expected collection to start with commits-, got %q", got)
	}
}
```

- [ ] **Step 2: Run the test to see it fail**

```bash
go test ./search/corpus/ -run TestCommitCorpus -v
```

Expected: FAIL (CommitCorpus not defined).

- [ ] **Step 3: Move git.go and adapt to the interface**

```bash
git mv internal/search/git.go search/corpus/commits.go
```

In the new file:
- Change `package search` to `package corpus`.
- Rename `CommitRecord` to a private internal type or keep it as-is.
- Add a `CommitCorpus` struct implementing `Corpus`:

```go
// CommitCorpus indexes git commit history (subject + body + diff summary).
type CommitCorpus struct {
	// Limit caps the number of commits enumerated; 0 means unlimited.
	Limit int
}

func (c *CommitCorpus) ID() string { return "commits" }

func (c *CommitCorpus) Collection(projectRoot string) string {
	// Must import search/qdrant for ProjectHash.
	return "commits-" + qdrant.ProjectHash(projectRoot)
}

func (c *CommitCorpus) Enumerate(projectRoot string) (<-chan Record, <-chan error) {
	out := make(chan Record)
	errs := make(chan error, 4)
	go func() {
		defer close(out)
		defer close(errs)
		commits, err := loadCommits(projectRoot, c.Limit)
		if err != nil {
			errs <- err
			return
		}
		for _, cm := range commits {
			out <- Record{
				Path:      cm.Hash,
				Kind:      "commit",
				EmbedText: commitEmbedText(cm),
				Extra: map[string]any{
					"hash":    cm.Hash,
					"date":    cm.Date.Format("2006-01-02"),
					"subject": cm.Subject,
				},
			}
		}
	}()
	return out, errs
}
```

Keep the existing `loadCommits` and `EmbedText` helpers; rename `EmbedText` method to `commitEmbedText` package-private helper.

- [ ] **Step 4: Delete the empty internal/search/ directory**

```bash
rm -rf internal/search
```

- [ ] **Step 5: Run the test**

```bash
go test ./search/corpus/ -run TestCommitCorpus -v
```

Expected: PASS.

- [ ] **Step 6: Run the full suite**

```bash
go build ./...
go test ./...
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "Extract CommitCorpus onto Corpus interface; remove internal/search"
```

---

### Task 1.5: Characterization test for `cspace search` back-compat

Ensure the existing commit-search CLI still works after the refactor. This test runs `cspace-go search --help` and verifies the output shape. End-to-end indexing is tested separately in Phase 7.

**Files:**
- Create: `internal/cli/search_test.go`

- [ ] **Step 1: Write the test**

```go
package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestSearchCommand_HelpShowsSubcommands(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"search", "--help"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("search --help: %v", err)
	}
	got := out.String()
	for _, want := range []string{"clusters", "index"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in help output, got:\n%s", want, got)
		}
	}
}
```

- [ ] **Step 2: Run and verify passes**

```bash
go test ./internal/cli/ -run TestSearchCommand_Help -v
```

Expected: PASS (assuming the search command still registers after the refactor).

- [ ] **Step 3: Commit**

```bash
git add internal/cli/search_test.go
git commit -m "Add characterization test for cspace search command surface"
```

---

## Phase 2 — Generic indexer, query, clustering

Goal: replace the commit-specific index/query/cluster code paths with ones that take a `Corpus`, so Phase 3's `CodeCorpus` drops in with no new plumbing.

### Task 2.1: Generic indexer

**Files:**
- Create: `search/index/index.go`
- Create: `search/index/index_test.go`
- Modify: `internal/cli/search.go` (update call site once indexer exists)

- [ ] **Step 1: Write the indexer contract test**

```go
package index

import (
	"context"
	"testing"

	"github.com/elliottregan/cspace/search/corpus"
)

// fakeCorpus emits a handful of records for testing.
type fakeCorpus struct {
	records []corpus.Record
}

func (f *fakeCorpus) ID() string                           { return "fake" }
func (f *fakeCorpus) Collection(_ string) string           { return "fake-test" }
func (f *fakeCorpus) Enumerate(_ string) (<-chan corpus.Record, <-chan error) {
	out := make(chan corpus.Record, len(f.records))
	errs := make(chan error)
	for _, r := range f.records {
		out <- r
	}
	close(out)
	close(errs)
	return out, errs
}

// fakeEmbedder returns a fixed vector per unique text.
type fakeEmbedder struct{ dim int }

func (e *fakeEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, e.dim)
		if len(t) > 0 {
			v[0] = float32(t[0])
		}
		out[i] = v
	}
	return out, nil
}

// fakeUpserter captures upserts in memory.
type fakeUpserter struct {
	collection string
	points     []Point
}

func (u *fakeUpserter) EnsureCollection(name string, dim int) error {
	u.collection = name
	return nil
}
func (u *fakeUpserter) UpsertPoints(_ string, pts []Point, _ int, _ func(int, int)) error {
	u.points = append(u.points, pts...)
	return nil
}
func (u *fakeUpserter) ExistingPoints(_ string) (map[uint64]string, error) {
	return map[uint64]string{}, nil
}
func (u *fakeUpserter) DeletePoints(_ string, _ []uint64) error { return nil }

func TestIndex_EmbedAndUpsertAllRecords(t *testing.T) {
	c := &fakeCorpus{records: []corpus.Record{
		{Path: "a", Kind: "chunk", EmbedText: "alpha", ContentHash: "h1"},
		{Path: "b", Kind: "chunk", EmbedText: "bravo", ContentHash: "h2"},
	}}
	e := &fakeEmbedder{dim: 4}
	u := &fakeUpserter{}
	err := Run(context.Background(), Config{
		Corpus:      c,
		Embedder:    e,
		Upserter:    u,
		ProjectRoot: ".",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(u.points) != 2 {
		t.Errorf("expected 2 points upserted, got %d", len(u.points))
	}
}

func TestIndex_SkipUnchangedByContentHash(t *testing.T) {
	c := &fakeCorpus{records: []corpus.Record{
		{Path: "a", Kind: "chunk", EmbedText: "alpha", ContentHash: "h1"},
	}}
	e := &fakeEmbedder{dim: 4}
	u := &fakeUpserter{}
	u.ExistingPoints = func(_ string) (map[uint64]string, error) {
		return map[uint64]string{corpus.Record{Path: "a", Kind: "chunk"}.ID(): "h1"}, nil
	}
	// NOTE: this test as written won't compile — it's here to pin the intended
	// interface. Refactor ExistingPoints into a method at implementation time.
	_ = u
	t.Skip("pinned for implementation")
}
```

- [ ] **Step 2: Run to verify compile error**

```bash
go test ./search/index/ -v
```

Expected: compile error — `Config`, `Run`, `Point` undefined.

- [ ] **Step 3: Implement the indexer**

```go
// Package index implements the generic indexer: embed records, upsert to
// Qdrant, delete orphans. It is corpus-agnostic.
package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/elliottregan/cspace/search/corpus"
)

// Point is the minimal vector+payload+id that the indexer hands to Upserter.
type Point struct {
	ID      uint64
	Vector  []float32
	Payload map[string]any
}

// Embedder embeds texts in batches and returns unit vectors.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Upserter writes Points to a vector store and can list existing IDs/hashes
// for change detection.
type Upserter interface {
	EnsureCollection(name string, dim int) error
	UpsertPoints(collection string, points []Point, batchSize int, progress func(done, total int)) error
	ExistingPoints(collection string) (map[uint64]string, error) // id -> content_hash
	DeletePoints(collection string, ids []uint64) error
}

// Config bundles the parts needed for one index run.
type Config struct {
	Corpus       corpus.Corpus
	Embedder     Embedder
	Upserter     Upserter
	ProjectRoot  string
	BatchSize    int // default 32
	Dim          int // default 768 (Jina v5 nano)
	LockPath     string
	Progress     func(done, total int)
}

// Run performs one end-to-end index pass: enumerate, embed changed records,
// upsert, delete orphans.
func Run(ctx context.Context, cfg Config) error {
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 32
	}
	if cfg.Dim == 0 {
		cfg.Dim = 768
	}
	collection := cfg.Corpus.Collection(cfg.ProjectRoot)

	if cfg.LockPath != "" {
		release, err := acquireLock(cfg.LockPath)
		if err != nil {
			return err // includes "already running" case
		}
		defer release()
	}

	if err := cfg.Upserter.EnsureCollection(collection, cfg.Dim); err != nil {
		return fmt.Errorf("ensure collection: %w", err)
	}
	existing, err := cfg.Upserter.ExistingPoints(collection)
	if err != nil {
		return fmt.Errorf("list existing: %w", err)
	}

	recs, errs := cfg.Corpus.Enumerate(cfg.ProjectRoot)
	go func() {
		for e := range errs {
			fmt.Fprintln(os.Stderr, "enumerate:", e)
		}
	}()

	seen := map[uint64]struct{}{}
	var toEmbed []corpus.Record

	for r := range recs {
		id := r.ID()
		seen[id] = struct{}{}
		if h, ok := existing[id]; ok && h == r.ContentHash {
			continue
		}
		toEmbed = append(toEmbed, r)
	}

	// Batch-embed.
	for i := 0; i < len(toEmbed); i += cfg.BatchSize {
		end := i + cfg.BatchSize
		if end > len(toEmbed) {
			end = len(toEmbed)
		}
		batch := toEmbed[i:end]
		texts := make([]string, len(batch))
		for j, r := range batch {
			texts[j] = r.EmbedText
		}
		vecs, err := cfg.Embedder.Embed(ctx, texts)
		if err != nil {
			return fmt.Errorf("embed batch %d: %w", i, err)
		}
		points := make([]Point, len(batch))
		for j, r := range batch {
			points[j] = Point{
				ID:      r.ID(),
				Vector:  vecs[j],
				Payload: payloadFor(r),
			}
		}
		if err := cfg.Upserter.UpsertPoints(collection, points, cfg.BatchSize, cfg.Progress); err != nil {
			return fmt.Errorf("upsert: %w", err)
		}
	}

	// Orphan cleanup.
	var orphans []uint64
	for id := range existing {
		if _, ok := seen[id]; !ok {
			orphans = append(orphans, id)
		}
	}
	if len(orphans) > 0 {
		if err := cfg.Upserter.DeletePoints(collection, orphans); err != nil {
			return fmt.Errorf("delete orphans: %w", err)
		}
	}

	return nil
}

func payloadFor(r corpus.Record) map[string]any {
	p := map[string]any{
		"path":         r.Path,
		"kind":         r.Kind,
		"line_start":   r.LineStart,
		"line_end":     r.LineEnd,
		"content_hash": r.ContentHash,
		"cluster_id":   -1,
	}
	for k, v := range r.Extra {
		p[k] = v
	}
	return p
}

// acquireLock uses O_EXCL to claim a lock file. Returns a release func.
func acquireLock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("index already running (lock %s)", path)
		}
		return nil, err
	}
	_ = f.Close()
	return func() { _ = os.Remove(path) }, nil
}
```

- [ ] **Step 4: Remove the skipped test and rerun**

Delete the `TestIndex_SkipUnchangedByContentHash` test body (the intent is implemented in `Run`; a proper skip test follows in Task 2.3).

```bash
go test ./search/index/ -v
```

Expected: `TestIndex_EmbedAndUpsertAllRecords` passes.

- [ ] **Step 5: Commit**

```bash
git add search/index/
git commit -m "Add generic indexer taking a Corpus"
```

---

### Task 2.2: Qdrant Upserter implementation

Wire the real Qdrant client to the `Upserter` interface.

**Files:**
- Modify: `search/qdrant/qdrant.go` — add `ExistingPoints` and `DeletePoints` methods
- Create: `search/qdrant/adapter.go` — shim satisfying `index.Upserter` using `*QdrantClient`

- [ ] **Step 1: Add ExistingPoints method to QdrantClient**

In `search/qdrant/qdrant.go`:

```go
// ExistingPoints returns a map of point ID → content_hash for a collection,
// paginating through scroll. Used by the indexer for change detection.
func (c *QdrantClient) ExistingPoints(collection string) (map[uint64]string, error) {
	out := map[uint64]string{}
	offset := any(nil)
	for {
		body := map[string]any{
			"limit":        256,
			"with_payload": []string{"content_hash"},
			"with_vector":  false,
		}
		if offset != nil {
			body["offset"] = offset
		}
		buf, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, c.BaseURL+"/collections/"+collection+"/points/scroll", bytes.NewReader(buf))
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("scroll: %w", err)
		}
		var parsed struct {
			Result struct {
				Points []struct {
					ID      uint64         `json:"id"`
					Payload map[string]any `json:"payload"`
				} `json:"points"`
				NextPageOffset any `json:"next_page_offset"`
			} `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
			_ = resp.Body.Close()
			return nil, err
		}
		_ = resp.Body.Close()
		for _, p := range parsed.Result.Points {
			if h, ok := p.Payload["content_hash"].(string); ok {
				out[p.ID] = h
			}
		}
		if parsed.Result.NextPageOffset == nil {
			break
		}
		offset = parsed.Result.NextPageOffset
	}
	return out, nil
}

// DeletePoints removes the listed point IDs.
func (c *QdrantClient) DeletePoints(collection string, ids []uint64) error {
	body, _ := json.Marshal(map[string]any{"points": ids})
	req, _ := http.NewRequest(http.MethodPost, c.BaseURL+"/collections/"+collection+"/points/delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("qdrant delete returned %d", resp.StatusCode)
	}
	return nil
}
```

- [ ] **Step 2: Create adapter satisfying index.Upserter**

Create `search/qdrant/adapter.go`:

```go
package qdrant

import "github.com/elliottregan/cspace/search/index"

// Adapter wraps *QdrantClient to satisfy index.Upserter.
// The adapter converts index.Point to QdrantPoint; the payload type widens
// from map[string]string to map[string]any at this boundary.
type Adapter struct{ *QdrantClient }

func (a *Adapter) UpsertPoints(collection string, points []index.Point, batchSize int, progress func(int, int)) error {
	qp := make([]QdrantPoint, len(points))
	for i, p := range points {
		qp[i] = QdrantPoint{ID: p.ID, Vector: p.Vector, Payload: p.Payload}
	}
	return a.QdrantClient.UpsertPoints(collection, qp, batchSize, progress)
}
```

- [ ] **Step 3: Build**

```bash
go build ./search/...
```

Expected: success.

- [ ] **Step 4: Commit**

```bash
git add search/qdrant/
git commit -m "Add Qdrant ExistingPoints/DeletePoints and index.Upserter adapter"
```

---

### Task 2.3: Embedder adapters

**Files:**
- Create: `search/embed/adapter.go`

- [ ] **Step 1: Write both adapters**

```go
package embed

import (
	"context"

	"github.com/elliottregan/cspace/search/index"
	"github.com/elliottregan/cspace/search/query"
)

// Adapter wraps *Client to satisfy index.Embedder (batch embedding).
type Adapter struct{ *Client }

func (a *Adapter) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return a.Client.Embed(ctx, texts)
}

// QueryAdapter wraps *Client to satisfy query.Embedder (single-query embedding
// with the retrieval-side prefix).
type QueryAdapter struct{ *Client }

func (a *QueryAdapter) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	// Jina v5 benefits from a task-specific prefix on queries; the Client
	// should prepend "Represent this for retrieval: " internally.
	vecs, err := a.Client.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, nil
	}
	return vecs[0], nil
}
```

- Verify the existing `*Client.Embed` accepts `context.Context`. If not, modify its signature now (call sites: the indexer and this adapter).
- If the existing `*Client` does not apply the retrieval prefix internally, add a `Mode` field to `*Client` or a dedicated `EmbedQuery` method there. Keep the prefix logic in the `embed` package, not in callers.

_Ensures both `index.Embedder` and `query.Embedder` interfaces have concrete implementations before later tasks reference them._

- [ ] **Step 2: Build**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 3: Commit**

```bash
git add search/embed/
git commit -m "Add Embedder adapter for indexer"
```

---

### Task 2.4: Generic query path

**Files:**
- Create: `search/query/query.go`
- Create: `search/query/query_test.go`

- [ ] **Step 1: Write the test**

```go
package query

import "testing"

func TestDedupeByPath_KeepsBestScorePerPath(t *testing.T) {
	hits := []Hit{
		{Path: "a.go", Score: 0.5, LineStart: 1, LineEnd: 50},
		{Path: "a.go", Score: 0.8, LineStart: 51, LineEnd: 100},
		{Path: "b.go", Score: 0.6, LineStart: 1, LineEnd: 30},
	}
	got := DedupeByPath(hits)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	// a.go wins with the higher-scoring chunk
	if got[0].Path != "a.go" || got[0].Score != 0.8 {
		t.Errorf("expected a.go@0.8 first, got %+v", got[0])
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test ./search/query/ -run TestDedupe -v
```

Expected: FAIL.

- [ ] **Step 3: Implement query**

```go
// Package query implements the generic query path: embed the query, ANN
// against Qdrant, dedupe by path, build the response envelope.
package query

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/elliottregan/cspace/search/corpus"
)

// Hit is a single ranked result.
type Hit struct {
	Path        string  `json:"path"`
	LineStart   int     `json:"line_start"`
	LineEnd     int     `json:"line_end"`
	Score       float32 `json:"score"`
	Kind        string  `json:"kind"`
	ContentHash string  `json:"content_hash"`
	Preview     string  `json:"preview,omitempty"`
	ClusterID   int     `json:"cluster_id,omitempty"`
}

// Envelope is the JSON response shape for a query.
type Envelope struct {
	Query     string `json:"query"`
	Corpus    string `json:"corpus"`
	Results   []Hit  `json:"results"`
	IndexedAt string `json:"indexed_at,omitempty"`
	Warning   string `json:"warning,omitempty"`
}

// Embedder embeds a single query string.
type Embedder interface {
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
}

// Searcher runs ANN against a Qdrant collection.
type Searcher interface {
	Search(collection string, vector []float32, topK int) ([]RawHit, error)
}

// RawHit is what the Searcher returns — Qdrant point with payload.
type RawHit struct {
	ID      uint64
	Score   float32
	Payload map[string]any
}

// Config bundles the pieces for a query.
type Config struct {
	Corpus      corpus.Corpus
	Embedder    Embedder
	Searcher    Searcher
	ProjectRoot string
	Query       string
	TopK        int
	WithCluster bool
}

// Run executes a single query.
func Run(ctx context.Context, cfg Config) (*Envelope, error) {
	if cfg.TopK <= 0 {
		cfg.TopK = 10
	}
	if cfg.TopK > 50 {
		cfg.TopK = 50
	}
	vec, err := cfg.Embedder.EmbedQuery(ctx, cfg.Query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if allZero(vec) {
		return &Envelope{
			Query:   cfg.Query,
			Corpus:  cfg.Corpus.ID(),
			Results: nil,
			Warning: "query produced a degenerate embedding; try a more specific phrase",
		}, nil
	}
	// Ask for 3× topK before dedupe to leave headroom.
	raws, err := cfg.Searcher.Search(cfg.Corpus.Collection(cfg.ProjectRoot), vec, cfg.TopK*3)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	hits := make([]Hit, 0, len(raws))
	for _, r := range raws {
		h := hitFromPayload(r)
		if !cfg.WithCluster {
			h.ClusterID = 0
		}
		hits = append(hits, h)
	}
	hits = DedupeByPath(hits)
	if len(hits) > cfg.TopK {
		hits = hits[:cfg.TopK]
	}
	return &Envelope{
		Query:     cfg.Query,
		Corpus:    cfg.Corpus.ID(),
		Results:   hits,
		IndexedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// DedupeByPath collapses multiple chunks of the same path, keeping the
// highest-scoring. Result is sorted by score descending.
func DedupeByPath(hits []Hit) []Hit {
	best := map[string]Hit{}
	for _, h := range hits {
		if cur, ok := best[h.Path]; !ok || h.Score > cur.Score {
			best[h.Path] = h
		}
	}
	out := make([]Hit, 0, len(best))
	for _, h := range best {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

func hitFromPayload(r RawHit) Hit {
	h := Hit{Score: r.Score}
	if v, ok := r.Payload["path"].(string); ok {
		h.Path = v
	}
	if v, ok := r.Payload["kind"].(string); ok {
		h.Kind = v
	}
	if v, ok := r.Payload["line_start"].(float64); ok {
		h.LineStart = int(v)
	}
	if v, ok := r.Payload["line_end"].(float64); ok {
		h.LineEnd = int(v)
	}
	if v, ok := r.Payload["content_hash"].(string); ok {
		h.ContentHash = v
	}
	if v, ok := r.Payload["cluster_id"].(float64); ok {
		h.ClusterID = int(v)
	}
	return h
}

func allZero(v []float32) bool {
	for _, x := range v {
		if x != 0 {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./search/query/ -v
```

Expected: PASS.

- [ ] **Step 5: Add Searcher implementation to qdrant**

In `search/qdrant/qdrant.go`, add:

```go
// SearchPoints performs an ANN query against a collection.
type SearchResult struct {
	ID      uint64
	Score   float32
	Payload map[string]any
}

func (c *QdrantClient) SearchPoints(collection string, vector []float32, topK int) ([]SearchResult, error) {
	body, _ := json.Marshal(map[string]any{
		"vector":       vector,
		"limit":        topK,
		"with_payload": true,
	})
	req, _ := http.NewRequest(http.MethodPost, c.BaseURL+"/collections/"+collection+"/points/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var parsed struct {
		Result []struct {
			ID      uint64         `json:"id"`
			Score   float32        `json:"score"`
			Payload map[string]any `json:"payload"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	out := make([]SearchResult, len(parsed.Result))
	for i, r := range parsed.Result {
		out[i] = SearchResult{ID: r.ID, Score: r.Score, Payload: r.Payload}
	}
	return out, nil
}
```

Add to `search/qdrant/adapter.go`:

```go
func (a *Adapter) Search(collection string, vector []float32, topK int) ([]query.RawHit, error) {
	raws, err := a.QdrantClient.SearchPoints(collection, vector, topK)
	if err != nil {
		return nil, err
	}
	out := make([]query.RawHit, len(raws))
	for i, r := range raws {
		out[i] = query.RawHit{ID: r.ID, Score: r.Score, Payload: r.Payload}
	}
	return out, nil
}
```

(Import `search/query` in the adapter file.)

- [ ] **Step 6: Build and test**

```bash
go build ./...
go test ./search/...
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add search/query/ search/qdrant/
git commit -m "Add generic query path with dedupe by path"
```

---

### Task 2.5: Cluster pipeline with cluster_id write-back

**Files:**
- Modify: `search/cluster/cluster.go` — expose `Run()` that reduces + clusters + writes `cluster_id` back to Qdrant payload.

- [ ] **Step 1: Implement cluster Run**

In `search/cluster/cluster.go`:

```go
// Run reduces the file-level vectors of a corpus to 2D, clusters them with
// HDBSCAN, and writes cluster_id back onto every Qdrant point whose payload
// path matches a clustered file. Returns the cluster map.
func Run(ctx context.Context, cfg Config) (*Result, error) {
	// 1. Scroll all points in collection, keep one per path (first by kind=file
	//    or lowest line_start for chunked files).
	// 2. Call reduce-api to get 2D coords.
	// 3. Call hdbscan-api to get labels.
	// 4. For each clustered path, update all payload points' cluster_id via
	//    Qdrant's set_payload.
	// 5. Return Result with {cluster_id → {size, density, top_paths}}.
}
```

Detailed implementation: the existing `internal/search/cluster.go` code before the move already does steps 2 and 3. Add steps 1, 4, 5. Step 4 uses Qdrant's `POST /collections/{c}/points/payload`:

```go
func (c *QdrantClient) SetPayload(collection string, ids []uint64, payload map[string]any) error {
	body, _ := json.Marshal(map[string]any{"payload": payload, "points": ids})
	req, _ := http.NewRequest(http.MethodPost, c.BaseURL+"/collections/"+collection+"/points/payload", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("set_payload returned %d", resp.StatusCode)
	}
	return nil
}
```

- [ ] **Step 2: Add unit test for per-path deduplication (step 1 above)**

```go
// search/cluster/cluster_test.go
package cluster

import "testing"

func TestPickRepresentative_PrefersFileKind(t *testing.T) {
	pts := []representative{
		{ID: 1, Path: "a.go", Kind: "chunk", LineStart: 50},
		{ID: 2, Path: "a.go", Kind: "file", LineStart: 0},
		{ID: 3, Path: "b.go", Kind: "chunk", LineStart: 1},
	}
	got := pickRepresentative(pts)
	if len(got) != 2 {
		t.Fatalf("expected 2 reps, got %d", len(got))
	}
	// a.go rep should be the file-kind point
	for _, r := range got {
		if r.Path == "a.go" && r.ID != 2 {
			t.Errorf("expected ID 2 for a.go, got %d", r.ID)
		}
	}
}
```

Implement `pickRepresentative` to select one point per path, preferring `kind=file`; if none, the lowest `line_start` chunk.

- [ ] **Step 3: Run tests**

```bash
go test ./search/cluster/ -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add search/cluster/
git commit -m "Cluster pipeline writes cluster_id back to Qdrant payload"
```

---

## Phase 3 — CodeCorpus

### Task 3.1: Git-tracked enumeration with filter

**Files:**
- Create: `search/corpus/code_filter.go`
- Create: `search/corpus/code_filter_test.go`

- [ ] **Step 1: Write tests**

```go
package corpus

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilter_SkipsBinary(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "image.png")
	os.WriteFile(bin, []byte{0x89, 0x50, 0x4e, 0x47, 0x00}, 0o644)
	f := DefaultFilter()
	if f.Accept(bin) {
		t.Error("filter should reject binary file")
	}
}

func TestFilter_SkipsOversized(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.txt")
	os.WriteFile(big, make([]byte, 300_000), 0o644)
	f := DefaultFilter()
	if f.Accept(big) {
		t.Error("filter should reject oversized file")
	}
}

func TestFilter_HonorsExcludeGlob(t *testing.T) {
	f := Filter{Excludes: []string{"vendor/**"}}
	if f.Accept("vendor/foo/bar.go") {
		t.Error("vendor/** should be rejected")
	}
	if !f.Accept("internal/foo.go") {
		t.Error("non-vendor path should pass")
	}
}
```

- [ ] **Step 2: Run to confirm fail**

```bash
go test ./search/corpus/ -run TestFilter -v
```

Expected: FAIL.

- [ ] **Step 3: Implement Filter**

```go
package corpus

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Filter decides whether a file should be indexed.
type Filter struct {
	MaxBytes int64
	Excludes []string // glob patterns relative to project root
}

// DefaultFilter returns sane defaults.
func DefaultFilter() Filter {
	return Filter{
		MaxBytes: 200 * 1024,
		Excludes: []string{
			"vendor/**",
			"internal/assets/embedded/**",
			"docs/superpowers/specs/**",
			"*.lock", "*.sum", "package-lock.json",
			"*.png", "*.jpg", "*.gif", "*.ico", "*.pdf", "*.zip", "*.tar.gz",
		},
	}
}

// Accept reports whether a project-root-relative path should be indexed.
func (f Filter) Accept(relPath string) bool {
	for _, g := range f.Excludes {
		matched, _ := matchGlob(g, relPath)
		if matched {
			return false
		}
	}
	info, err := os.Stat(relPath)
	if err != nil {
		return false
	}
	if f.MaxBytes > 0 && info.Size() > f.MaxBytes {
		return false
	}
	// Binary check: read first 1 KB, reject if contains a null byte.
	fh, err := os.Open(relPath)
	if err != nil {
		return false
	}
	defer func() { _ = fh.Close() }()
	buf := make([]byte, 1024)
	n, _ := io.ReadFull(fh, buf)
	if n > 0 && bytes.Contains(buf[:n], []byte{0}) {
		return false
	}
	return true
}

// matchGlob supports ** (doublestar) in addition to stdlib path.Match syntax.
func matchGlob(pattern, path string) (bool, error) {
	// Quick-and-simple doublestar: split on ** and anchor.
	if strings.Contains(pattern, "**") {
		parts := strings.SplitN(pattern, "**", 2)
		prefix := strings.TrimSuffix(parts[0], "/")
		suffix := strings.TrimPrefix(parts[1], "/")
		if prefix != "" && !strings.HasPrefix(path, prefix) {
			return false, nil
		}
		if suffix == "" {
			return true, nil
		}
		return filepath.Match(suffix, filepath.Base(path))
	}
	return filepath.Match(pattern, path)
}
```

- [ ] **Step 4: Tests pass**

```bash
go test ./search/corpus/ -run TestFilter -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add search/corpus/code_filter.go search/corpus/code_filter_test.go
git commit -m "Add file filter for CodeCorpus: binary, size, exclude globs"
```

---

### Task 3.2: Chunker with line ranges

**Files:**
- Create: `search/corpus/code_chunker.go`
- Create: `search/corpus/code_chunker_test.go`

- [ ] **Step 1: Write tests**

```go
package corpus

import (
	"strings"
	"testing"
)

func TestChunk_SmallFile_OneChunkWholeFile(t *testing.T) {
	content := "line1\nline2\nline3\n"
	chunks := Chunk([]byte(content), ChunkConfig{Max: 12000, Overlap: 0})
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].LineStart != 1 || chunks[0].LineEnd != 3 {
		t.Errorf("expected lines 1-3, got %d-%d", chunks[0].LineStart, chunks[0].LineEnd)
	}
	if chunks[0].Text != content {
		t.Errorf("text differs")
	}
}

func TestChunk_LargeFile_SplitsWithOverlap(t *testing.T) {
	// Build a 20k-char file with predictable line markers every 100 chars.
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString(strings.Repeat("x", 99))
		b.WriteString("\n")
	}
	content := b.String()
	chunks := Chunk([]byte(content), ChunkConfig{Max: 8000, Overlap: 200})
	if len(chunks) < 2 {
		t.Fatalf("expected ≥2 chunks, got %d", len(chunks))
	}
	// Line 1 must appear in the first chunk.
	if chunks[0].LineStart != 1 {
		t.Errorf("first chunk should start at line 1, got %d", chunks[0].LineStart)
	}
	// Consecutive chunks overlap in line numbers by at least 1 line.
	for i := 1; i < len(chunks); i++ {
		if chunks[i].LineStart > chunks[i-1].LineEnd {
			t.Errorf("chunk %d starts at line %d after previous ended at %d — no overlap",
				i, chunks[i].LineStart, chunks[i-1].LineEnd)
		}
	}
}
```

- [ ] **Step 2: Run to fail**

```bash
go test ./search/corpus/ -run TestChunk -v
```

Expected: FAIL.

- [ ] **Step 3: Implement Chunk**

```go
package corpus

import "strings"

// ChunkConfig tunes the chunker.
type ChunkConfig struct {
	Max     int // max chars per chunk
	Overlap int // chars of overlap between consecutive chunks
}

// Chunk is one contiguous text slice with a line range.
type ChunkOut struct {
	Text      string
	LineStart int // 1-based inclusive
	LineEnd   int // 1-based inclusive
}

// Chunk splits content into ChunkOut slices respecting max size and overlap.
// Line numbers are preserved.
func Chunk(content []byte, cfg ChunkConfig) []ChunkOut {
	s := string(content)
	if len(s) <= cfg.Max {
		return []ChunkOut{{Text: s, LineStart: 1, LineEnd: lineCount(s)}}
	}
	var out []ChunkOut
	start := 0
	for start < len(s) {
		end := start + cfg.Max
		if end > len(s) {
			end = len(s)
		} else {
			// Snap end to the nearest newline within the last 200 chars to avoid
			// mid-line cuts when possible.
			if nl := strings.LastIndex(s[start:end], "\n"); nl > cfg.Max-500 {
				end = start + nl + 1
			}
		}
		text := s[start:end]
		ls := lineOf(s, start)
		le := lineOf(s, end-1)
		out = append(out, ChunkOut{Text: text, LineStart: ls, LineEnd: le})
		if end >= len(s) {
			break
		}
		// Advance, keeping `Overlap` chars of backtrack.
		start = end - cfg.Overlap
	}
	return out
}

func lineCount(s string) int {
	n := strings.Count(s, "\n")
	if len(s) > 0 && !strings.HasSuffix(s, "\n") {
		n++
	}
	if n == 0 {
		n = 1
	}
	return n
}

func lineOf(s string, off int) int {
	if off >= len(s) {
		off = len(s) - 1
	}
	if off < 0 {
		return 1
	}
	return 1 + strings.Count(s[:off+1], "\n")
}
```

- [ ] **Step 4: Tests pass**

```bash
go test ./search/corpus/ -run TestChunk -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add search/corpus/code_chunker.go search/corpus/code_chunker_test.go
git commit -m "Add chunker with line-range tracking and overlap"
```

---

### Task 3.3: CodeCorpus Enumerate

**Files:**
- Create: `search/corpus/code.go`
- Create: `search/corpus/code_test.go`

- [ ] **Step 1: Write the integration test**

```go
package corpus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Set up a throwaway git repo, add files, enumerate.
func TestCodeCorpus_EnumeratesGitTrackedTextFiles(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "image.png"), []byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0x01, 0x02}, 0o644)
	run("add", "-A")
	run("commit", "-qm", "init")

	cc := &CodeCorpus{Filter: DefaultFilter(), Chunk: ChunkConfig{Max: 12000, Overlap: 200}}
	recs, errs := cc.Enumerate(dir)
	go func() {
		for range errs {
		}
	}()
	var paths []string
	for r := range recs {
		paths = append(paths, r.Path)
		if r.ContentHash == "" {
			t.Errorf("record missing ContentHash: %+v", r)
		}
	}
	if len(paths) != 1 || !strings.HasSuffix(paths[0], "hello.go") {
		t.Errorf("expected only hello.go, got %v", paths)
	}
}

func TestCodeCorpus_CollectionName(t *testing.T) {
	cc := &CodeCorpus{}
	n := cc.Collection(".")
	if !strings.HasPrefix(n, "code-") {
		t.Errorf("expected code- prefix, got %q", n)
	}
}
```

- [ ] **Step 2: Run to fail**

```bash
go test ./search/corpus/ -run TestCodeCorpus -v
```

Expected: FAIL.

- [ ] **Step 3: Implement CodeCorpus**

```go
package corpus

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/elliottregan/cspace/search/qdrant"
)

// CodeCorpus indexes git-tracked source files.
type CodeCorpus struct {
	Filter Filter
	Chunk  ChunkConfig
}

func (c *CodeCorpus) ID() string { return "code" }

func (c *CodeCorpus) Collection(projectRoot string) string {
	return "code-" + qdrant.ProjectHash(projectRoot)
}

func (c *CodeCorpus) Enumerate(projectRoot string) (<-chan Record, <-chan error) {
	out := make(chan Record)
	errs := make(chan error, 8)
	go func() {
		defer close(out)
		defer close(errs)

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "git", "-C", projectRoot, "ls-files")
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			errs <- fmt.Errorf("git ls-files: %w", err)
			return
		}
		if err := cmd.Start(); err != nil {
			errs <- fmt.Errorf("git ls-files start: %w", err)
			return
		}

		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			rel := sc.Text()
			abs := filepath.Join(projectRoot, rel)
			if !c.Filter.Accept(abs) {
				continue
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				errs <- fmt.Errorf("read %s: %w", rel, err)
				continue
			}
			hash := fmt.Sprintf("%x", sha256.Sum256(data))
			chunks := Chunk(data, c.Chunk)
			kind := "file"
			if len(chunks) > 1 {
				kind = "chunk"
			}
			for _, ch := range chunks {
				rec := Record{
					Path:        rel,
					Kind:        kind,
					LineStart:   ch.LineStart,
					LineEnd:     ch.LineEnd,
					ContentHash: hash,
					EmbedText:   formatCodeEmbedText(rel, ch.Text),
					Extra: map[string]any{
						"mtime": time.Now().UTC().Format(time.RFC3339),
					},
				}
				out <- rec
			}
		}
		if err := sc.Err(); err != nil {
			errs <- fmt.Errorf("scan: %w", err)
		}
		_ = cmd.Wait()
	}()
	return out, errs
}

// formatCodeEmbedText prepends a small header so the embedder sees the path
// context. Jina v5 benefits from this signal.
func formatCodeEmbedText(path, body string) string {
	const max = 12000
	header := "File: " + path + "\n\n"
	if len(body)+len(header) > max {
		body = body[:max-len(header)]
	}
	return header + strings.TrimRight(body, "\x00")
}
```

- [ ] **Step 4: Tests pass**

```bash
go test ./search/corpus/ -run TestCodeCorpus -v
```

Expected: PASS.

- [ ] **Step 5: Run the whole suite**

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add search/corpus/code.go search/corpus/code_test.go
git commit -m "Add CodeCorpus enumerating git-tracked files with chunking and hashing"
```

---

## Phase 4 — Config and standalone CLI binary

### Task 4.1: search.yaml config + defaults

**Files:**
- Create: `search/config/config.go`
- Create: `search/config/config_test.go`
- Create: `search/config/default.yaml` (embedded via `go:embed`)

- [ ] **Step 1: Add the default.yaml**

```yaml
# search/config/default.yaml
# Default corpus configuration for cspace-search.
# Projects override by placing a search.yaml at the project root.
corpora:
  code:
    enabled: true
    max_bytes: 204800
    excludes:
      - "vendor/**"
      - "internal/assets/embedded/**"
      - "docs/superpowers/specs/**"
      - "*.lock"
      - "*.sum"
      - "package-lock.json"
      - "*.png"
      - "*.jpg"
      - "*.gif"
      - "*.ico"
      - "*.pdf"
      - "*.zip"
      - "*.tar.gz"
  commits:
    enabled: true
    limit: 500
sidecars:
  llama_retrieval_url: "http://llama-server:8080"
  llama_clustering_url: "http://llama-clustering:8080"
  qdrant_url: "http://qdrant:6333"
  reduce_url: "http://reduce-api:8000"
  hdbscan_url: "http://hdbscan-api:8090"
index:
  lock_path: ".cspace/search-index.lock"
  log_path: ".cspace/search-index.log"
```

- [ ] **Step 2: Write tests**

```go
package config

import "testing"

func TestLoad_Defaults(t *testing.T) {
	c, err := Load(".")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Corpora["code"].MaxBytes != 204800 {
		t.Errorf("expected MaxBytes 204800, got %d", c.Corpora["code"].MaxBytes)
	}
}

func TestLoad_ProjectOverride(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "search.yaml"), []byte(`
corpora:
  code:
    max_bytes: 50000
`), 0o644)
	c, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Corpora["code"].MaxBytes != 50000 {
		t.Errorf("expected override 50000, got %d", c.Corpora["code"].MaxBytes)
	}
	// Non-overridden values still come from defaults.
	if c.Sidecars.QdrantURL == "" {
		t.Errorf("expected defaulted QdrantURL, got empty")
	}
}
```

- [ ] **Step 3: Run to fail**

```bash
go test ./search/config/ -v
```

Expected: FAIL — `Load` undefined.

- [ ] **Step 4: Implement config**

```go
// Package config loads search.yaml with defaults merged in.
package config

import (
	_ "embed"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

//go:embed default.yaml
var defaultYAML []byte

// Config is the top-level config shape.
type Config struct {
	Corpora  map[string]CorpusConfig `yaml:"corpora"`
	Sidecars Sidecars                `yaml:"sidecars"`
	Index    IndexConfig             `yaml:"index"`
}

type CorpusConfig struct {
	Enabled  bool     `yaml:"enabled"`
	MaxBytes int64    `yaml:"max_bytes"`
	Excludes []string `yaml:"excludes"`
	Limit    int      `yaml:"limit"`
}

type Sidecars struct {
	LlamaRetrievalURL  string `yaml:"llama_retrieval_url"`
	LlamaClusteringURL string `yaml:"llama_clustering_url"`
	QdrantURL          string `yaml:"qdrant_url"`
	ReduceURL          string `yaml:"reduce_url"`
	HDBSCANURL         string `yaml:"hdbscan_url"`
}

type IndexConfig struct {
	LockPath string `yaml:"lock_path"`
	LogPath  string `yaml:"log_path"`
}

// Load reads defaults + projectRoot/search.yaml if present.
func Load(projectRoot string) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(defaultYAML, &cfg); err != nil {
		return nil, err
	}
	path := filepath.Join(projectRoot, "search.yaml")
	if b, err := os.ReadFile(path); err == nil {
		// Shallow-merge: project config overrides defaults on present fields.
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			return nil, err
		}
	}
	return &cfg, nil
}
```

Add `gopkg.in/yaml.v3` to go.mod:

```bash
go get gopkg.in/yaml.v3@latest
```

- [ ] **Step 5: Tests pass**

```bash
go test ./search/config/ -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add search/config/ go.mod go.sum
git commit -m "Add search.yaml config loader with embedded defaults"
```

---

### Task 4.2: `cspace-search` standalone binary

**Files:**
- Create: `cmd/cspace-search/main.go`

- [ ] **Step 1: Scaffold the binary**

```go
// Package main implements cspace-search, a standalone CLI for semantic search
// over commits and code. Produced alongside cspace-go so this binary can be
// dropped into any repo with a search.yaml.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/elliottregan/cspace/search/cluster"
	"github.com/elliottregan/cspace/search/config"
	"github.com/elliottregan/cspace/search/corpus"
	"github.com/elliottregan/cspace/search/embed"
	"github.com/elliottregan/cspace/search/index"
	"github.com/elliottregan/cspace/search/qdrant"
	"github.com/elliottregan/cspace/search/query"
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{Use: "cspace-search", Short: "Semantic search over commits and code"}
	root.AddCommand(indexCmd(), queryCmd(), clustersCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func projectRoot() string {
	// Prefer CWD; fall back to git toplevel if inside a repo.
	cwd, _ := os.Getwd()
	out, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return cwd
	}
	return filepath.Clean(string(bytesTrim(out)))
}

func bytesTrim(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func loadCorpus(id string, cfg *config.Config) (corpus.Corpus, error) {
	switch id {
	case "code":
		cc := &corpus.CodeCorpus{
			Filter: corpus.Filter{
				MaxBytes: cfg.Corpora["code"].MaxBytes,
				Excludes: cfg.Corpora["code"].Excludes,
			},
			Chunk: corpus.ChunkConfig{Max: 12000, Overlap: 200},
		}
		return cc, nil
	case "commits":
		return &corpus.CommitCorpus{Limit: cfg.Corpora["commits"].Limit}, nil
	}
	return nil, fmt.Errorf("unknown corpus %q", id)
}

func indexCmd() *cobra.Command {
	var corpusID string
	var quiet bool
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Build or refresh an index",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(projectRoot())
			if err != nil {
				return err
			}
			c, err := loadCorpus(corpusID, cfg)
			if err != nil {
				return err
			}
			qc := qdrant.NewQdrantClient(cfg.Sidecars.QdrantURL)
			ec := embed.NewClient(cfg.Sidecars.LlamaRetrievalURL)
			progress := func(done, total int) {
				if !quiet {
					fmt.Fprintf(os.Stderr, "\rindex: %d/%d", done, total)
				}
			}
			return index.Run(context.Background(), index.Config{
				Corpus:      c,
				Embedder:    &embed.Adapter{Client: ec},
				Upserter:    &qdrant.Adapter{QdrantClient: qc},
				ProjectRoot: projectRoot(),
				LockPath:    filepath.Join(projectRoot(), cfg.Index.LockPath),
				Progress:    progress,
			})
		},
	}
	cmd.Flags().StringVar(&corpusID, "corpus", "code", "corpus id (code|commits)")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "suppress progress")
	return cmd
}

func queryCmd() *cobra.Command {
	var corpusID string
	var topK int
	var asJSON bool
	var withCluster bool
	cmd := &cobra.Command{
		Use:   "query <query>",
		Short: "Run a semantic query",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(projectRoot())
			if err != nil {
				return err
			}
			c, err := loadCorpus(corpusID, cfg)
			if err != nil {
				return err
			}
			qc := qdrant.NewQdrantClient(cfg.Sidecars.QdrantURL)
			ec := embed.NewClient(cfg.Sidecars.LlamaRetrievalURL)
			q := args[0]
			env, err := query.Run(context.Background(), query.Config{
				Corpus:      c,
				Embedder:    &embed.QueryAdapter{Client: ec},
				Searcher:    &qdrant.Adapter{QdrantClient: qc},
				ProjectRoot: projectRoot(),
				Query:       q,
				TopK:        topK,
				WithCluster: withCluster,
			})
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(env)
			}
			for _, h := range env.Results {
				fmt.Printf("%.3f  %s:%d-%d  (%s)\n", h.Score, h.Path, h.LineStart, h.LineEnd, h.Kind)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&corpusID, "corpus", "code", "corpus id")
	cmd.Flags().IntVar(&topK, "top", 10, "top K hits")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON envelope")
	cmd.Flags().BoolVar(&withCluster, "with-cluster", false, "include cluster_id per hit")
	return cmd
}

func clustersCmd() *cobra.Command {
	var corpusID string
	var coordsOut string
	cmd := &cobra.Command{
		Use:   "clusters",
		Short: "Discover thematic clusters and write cluster_id to the index",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(projectRoot())
			if err != nil {
				return err
			}
			c, err := loadCorpus(corpusID, cfg)
			if err != nil {
				return err
			}
			return cluster.Run(context.Background(), cluster.Config{
				Corpus:       c,
				ProjectRoot:  projectRoot(),
				QdrantURL:    cfg.Sidecars.QdrantURL,
				LlamaURL:     cfg.Sidecars.LlamaClusteringURL,
				ReduceURL:    cfg.Sidecars.ReduceURL,
				HDBSCANURL:   cfg.Sidecars.HDBSCANURL,
				CoordsOutput: coordsOut,
			})
		},
	}
	cmd.Flags().StringVar(&corpusID, "corpus", "code", "corpus id")
	cmd.Flags().StringVar(&coordsOut, "coords-out", "", "write TSV of (hash|path, x, y, label)")
	return cmd
}
```

Notes for the implementer:
- `embed.Adapter` and `embed.QueryAdapter` may need adding if they don't exist yet. `Adapter` satisfies `index.Embedder`; `QueryAdapter` satisfies `query.Embedder`. Both wrap the same `embed.Client`.
- `cluster.Config` and `cluster.Run` need the fields shown here (URLs + coords output).
- If any of those don't exist yet, add them with minimal logic in this task.

- [ ] **Step 2: Build**

```bash
go build ./cmd/cspace-search/
```

Expected: success.

- [ ] **Step 3: Smoke test against the cspace repo itself**

```bash
./cspace-search --help
./cspace-search index --corpus=commits --quiet   # requires sidecars; OK to skip if unavailable
```

Expected: help output lists `index`, `query`, `clusters`. If sidecars unavailable, index prints a clear error naming the unreachable service.

- [ ] **Step 4: Commit**

```bash
git add cmd/cspace-search/ search/embed/
git commit -m "Add cspace-search standalone CLI"
```

---

## Phase 5 — MCP server binary

### Task 5.1: Scaffold cspace-search-mcp

**Files:**
- Create: `cmd/cspace-search-mcp/main.go`
- Create: `search/mcp/server.go`
- Create: `search/mcp/server_test.go`

- [ ] **Step 1: Implement the server**

`search/mcp/server.go`:

```go
// Package mcp exposes search tools via the Model Context Protocol.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/elliottregan/cspace/search/cluster"
	"github.com/elliottregan/cspace/search/config"
	"github.com/elliottregan/cspace/search/corpus"
	"github.com/elliottregan/cspace/search/embed"
	"github.com/elliottregan/cspace/search/qdrant"
	"github.com/elliottregan/cspace/search/query"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server holds dependencies for tool handlers.
type Server struct {
	ProjectRoot string
	Config      *config.Config
}

// Register attaches search_code and list_clusters to the provided MCP server.
func (s *Server) Register(srv *mcp.Server) {
	srv.AddTool(&mcp.Tool{
		Name:        "search_code",
		Description: "Semantic search over the current codebase. Returns ranked file chunks (path + line range) for a natural-language concept. Use during exploration before making a change to understand where a concern is implemented.",
		InputSchema: mustSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":        map[string]any{"type": "string"},
				"top_k":        map[string]any{"type": "integer", "default": 10, "maximum": 50},
				"with_cluster": map[string]any{"type": "boolean", "default": false},
			},
			"required": []string{"query"},
		}),
	}, s.handleSearchCode)

	srv.AddTool(&mcp.Tool{
		Name:        "list_clusters",
		Description: "List architectural clusters of the code corpus. Each cluster contains files that share a concern.",
		InputSchema: mustSchema(map[string]any{"type": "object", "properties": map[string]any{}}),
	}, s.handleListClusters)
}

func (s *Server) handleSearchCode(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in struct {
		Query       string `json:"query"`
		TopK        int    `json:"top_k"`
		WithCluster bool   `json:"with_cluster"`
	}
	if err := json.Unmarshal(req.Arguments, &in); err != nil {
		return nil, err
	}
	cc := &corpus.CodeCorpus{
		Filter: corpus.Filter{MaxBytes: s.Config.Corpora["code"].MaxBytes, Excludes: s.Config.Corpora["code"].Excludes},
		Chunk:  corpus.ChunkConfig{Max: 12000, Overlap: 200},
	}
	qc := qdrant.NewQdrantClient(s.Config.Sidecars.QdrantURL)
	ec := embed.NewClient(s.Config.Sidecars.LlamaRetrievalURL)
	env, err := query.Run(ctx, query.Config{
		Corpus:      cc,
		Embedder:    &embed.QueryAdapter{Client: ec},
		Searcher:    &qdrant.Adapter{QdrantClient: qc},
		ProjectRoot: s.ProjectRoot,
		Query:       in.Query,
		TopK:        in.TopK,
		WithCluster: in.WithCluster,
	})
	if err != nil {
		return nil, err
	}
	buf, _ := json.Marshal(env)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(buf)}}}, nil
}

func (s *Server) handleListClusters(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cc := &corpus.CodeCorpus{}
	m, err := cluster.List(ctx, cluster.Config{
		Corpus:      cc,
		ProjectRoot: s.ProjectRoot,
		QdrantURL:   s.Config.Sidecars.QdrantURL,
	})
	if err != nil {
		return nil, err
	}
	buf, _ := json.Marshal(m)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(buf)}}}, nil
}

func mustSchema(v any) *mcp.Schema {
	s, err := mcp.SchemaFrom(v)
	if err != nil {
		panic(err)
	}
	return s
}
```

Note: `cluster.List` is a new function that scrolls existing Qdrant payload and returns `{cluster_id → {size, top_paths}}` without re-running the pipeline. Add it to `search/cluster/cluster.go`:

```go
// List returns the current cluster map by scrolling Qdrant payloads.
// Does not re-run the reduce/HDBSCAN pipeline.
type ClusterSummary struct {
	ClusterID int      `json:"cluster_id"`
	Size      int      `json:"size"`
	TopPaths  []string `json:"top_paths"`
}

func List(ctx context.Context, cfg Config) ([]ClusterSummary, error) {
	qc := qdrant.NewQdrantClient(cfg.QdrantURL)
	collection := cfg.Corpus.Collection(cfg.ProjectRoot)
	// Scroll all points; group by cluster_id; top 6 paths per cluster (by
	// arbitrary order for now — can sort by centrality later).
	...
}
```

- [ ] **Step 2: Write the main binary**

`cmd/cspace-search-mcp/main.go`:

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/elliottregan/cspace/search/config"
	"github.com/elliottregan/cspace/search/mcp"

	mcpSDK "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	root := projectRoot()
	cfg, err := config.Load(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	server := mcpSDK.NewServer(&mcpSDK.Implementation{Name: "cspace-search", Version: "0.1.0"}, nil)
	(&mcp.Server{ProjectRoot: root, Config: cfg}).Register(server)
	if err := server.Run(context.Background(), mcpSDK.NewStdioTransport()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func projectRoot() string {
	cwd, _ := os.Getwd()
	out, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return cwd
	}
	return filepath.Clean(string(trimTail(out)))
}

func trimTail(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
```

- [ ] **Step 3: Write contract test**

`search/mcp/server_test.go`:

```go
package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/elliottregan/cspace/search/config"

	mcpSDK "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServer_RegistersBothTools(t *testing.T) {
	srv := mcpSDK.NewServer(&mcpSDK.Implementation{Name: "test", Version: "0"}, nil)
	(&Server{ProjectRoot: ".", Config: &config.Config{}}).Register(srv)
	tools := srv.Tools()
	names := map[string]bool{}
	for _, to := range tools {
		names[to.Name] = true
	}
	for _, want := range []string{"search_code", "list_clusters"} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
	for _, to := range tools {
		if !strings.Contains(to.Description, "codebase") && !strings.Contains(to.Description, "clusters") {
			t.Errorf("tool %q description is too vague: %q", to.Name, to.Description)
		}
	}
	_ = context.Background() // placeholder
}
```

- [ ] **Step 4: Build + test**

```bash
go build ./cmd/cspace-search-mcp/
go test ./search/mcp/ -v
```

Expected: build succeeds; test passes.

- [ ] **Step 5: Commit**

```bash
git add cmd/cspace-search-mcp/ search/mcp/
git commit -m "Add cspace-search-mcp with search_code and list_clusters tools"
```

---

## Phase 6 — Cspace CLI shim

### Task 6.1: Refactor `cspace search` to in-process call

**Files:**
- Modify: `internal/cli/search.go`

- [ ] **Step 1: Replace the search command with shim dispatching by corpus**

The new `internal/cli/search.go` builds a tree: `search → {code, commits, clusters}` where `code` and `commits` have `query`, `index`, `clusters` subcommands. The `search <query>` form remains as an alias for `search commits query <query>` for back-compat.

Use the same `query.Run`, `index.Run`, `cluster.Run` functions as `cspace-search` — in-process import. Wire them up to cobra:

```go
package cli

import (
	"github.com/elliottregan/cspace/search/config"
	"github.com/elliottregan/cspace/search/corpus"
	"github.com/spf13/cobra"
	// ... plus index, query, cluster, embed, qdrant
)

func newSearchCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "search",
		Short: "Semantic search over commits and code",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Back-compat: `cspace search "<query>"` runs commits query.
			if len(args) == 1 {
				return runQuery(cmd.Context(), "commits", args[0], 10, false, false)
			}
			return cmd.Help()
		},
	}
	root.AddCommand(newSearchCodeCmd(), newSearchCommitsCmd())
	return root
}

func newSearchCodeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "code", Short: "Search the current codebase"}
	cmd.AddCommand(newCorpusQueryCmd("code"), newCorpusIndexCmd("code"), newCorpusClustersCmd("code"))
	return cmd
}
func newSearchCommitsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "commits", Short: "Search commit history"}
	cmd.AddCommand(newCorpusQueryCmd("commits"), newCorpusIndexCmd("commits"), newCorpusClustersCmd("commits"))
	return cmd
}
```

Implement `runQuery`, `newCorpusQueryCmd`, etc., as thin wrappers that construct the same dependencies `cspace-search` builds. Extract the shared construction into a helper in `search/config/runtime.go` so both binaries use it:

```go
// search/config/runtime.go
package config

import (
	"github.com/elliottregan/cspace/search/corpus"
	"github.com/elliottregan/cspace/search/embed"
	"github.com/elliottregan/cspace/search/qdrant"
)

type Runtime struct {
	Corpus   corpus.Corpus
	Qdrant   *qdrant.QdrantClient
	Embed    *embed.Client
	ProjRoot string
	Cfg      *Config
}

func Build(projectRoot, corpusID string) (*Runtime, error) {
	cfg, err := Load(projectRoot)
	if err != nil {
		return nil, err
	}
	c, err := buildCorpus(corpusID, cfg)
	if err != nil {
		return nil, err
	}
	return &Runtime{
		Corpus:   c,
		Qdrant:   qdrant.NewQdrantClient(cfg.Sidecars.QdrantURL),
		Embed:    embed.NewClient(cfg.Sidecars.LlamaRetrievalURL),
		ProjRoot: projectRoot,
		Cfg:      cfg,
	}, nil
}

func buildCorpus(id string, cfg *Config) (corpus.Corpus, error) { /* ... */ }
```

Both `cmd/cspace-search/main.go` and `internal/cli/search.go` use `config.Build(...)`.

- [ ] **Step 2: Build**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 3: Verify the characterization test still passes**

```bash
go test ./internal/cli/ -run TestSearchCommand_Help -v
```

Expected: PASS.

- [ ] **Step 4: Smoke-test back-compat**

```bash
./bin/cspace-go search --help | grep -E "code|commits"
./bin/cspace-go search code --help
```

Expected: help lists `code` and `commits` subcommands.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/search.go search/config/runtime.go
git commit -m "Refactor cspace search into in-process wrapper over search/"
```

---

## Phase 7 — Infrastructure and integration

### Task 7.1: docker-compose.search.yml

**Files:**
- Create: `lib/templates/docker-compose.search.yml`
- Modify: `lib/templates/docker-compose.core.yml`

- [ ] **Step 1: Create the search compose file**

```yaml
# lib/templates/docker-compose.search.yml
# Adds cspace-search-mcp as a sidecar. The other search infra (llama-server,
# qdrant, reduce-api, hdbscan-api) is defined in docker-compose.core.yml since
# the commit indexer shares it.
services:
  cspace-search-mcp:
    image: ${CSPACE_IMAGE:-cspace:latest}
    container_name: ${CSPACE_CONTAINER_NAME}.search-mcp
    depends_on:
      - llama-server
      - qdrant
    command: ["/usr/local/bin/cspace-search-mcp"]
    working_dir: /workspace
    volumes:
      - ../..:/workspace:ro
      - ../../.cspace:/workspace/.cspace
    stdin_open: true
    tty: false
    restart: unless-stopped
```

- [ ] **Step 2: Include from core compose**

In `lib/templates/docker-compose.core.yml`, add an `include:` at the top:

```yaml
include:
  - docker-compose.search.yml
```

If Docker Compose version does not support `include`, import via `COMPOSE_FILE` env instead; Go code in `internal/compose/` should already handle multiple files.

- [ ] **Step 3: Verify compose file parses**

```bash
docker compose -f lib/templates/docker-compose.core.yml config > /dev/null
```

Expected: no error.

- [ ] **Step 4: Commit**

```bash
git add lib/templates/docker-compose.search.yml lib/templates/docker-compose.core.yml
git commit -m "Add docker-compose.search.yml with cspace-search-mcp sidecar"
```

---

### Task 7.2: Makefile updates

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Add build targets**

Locate the existing `build:` target. Add:

```makefile
BINS := cspace-go cspace-search cspace-search-mcp

.PHONY: build
build: $(addprefix bin/,$(BINS))

bin/cspace-go: $(shell find cmd/cspace internal -name '*.go') sync-embedded
	go build -o $@ ./cmd/cspace

bin/cspace-search: $(shell find cmd/cspace-search search -name '*.go')
	go build -o $@ ./cmd/cspace-search

bin/cspace-search-mcp: $(shell find cmd/cspace-search-mcp search -name '*.go')
	go build -o $@ ./cmd/cspace-search-mcp
```

- [ ] **Step 2: Build all binaries**

```bash
make build
ls bin/
```

Expected: three binaries.

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "Build cspace-search and cspace-search-mcp"
```

---

### Task 7.3: Lefthook hooks for freshness

**Files:**
- Modify: `lefthook.yml`
- Modify: `.cspace/.gitignore`

- [ ] **Step 1: Add hook entries**

Append to `lefthook.yml`:

```yaml
post-commit:
  commands:
    search-index:
      run: ( ./bin/cspace-search index --corpus=code --quiet >> .cspace/search-index.log 2>&1 & )

post-checkout:
  commands:
    search-index:
      run: ( ./bin/cspace-search index --corpus=code --quiet >> .cspace/search-index.log 2>&1 & )

post-merge:
  commands:
    search-index:
      run: ( ./bin/cspace-search index --corpus=code --quiet >> .cspace/search-index.log 2>&1 & )
```

- [ ] **Step 2: Ignore lock + log**

In `.cspace/.gitignore` (create if absent):

```
search-index.lock
search-index.log
```

- [ ] **Step 3: Verify lefthook registers hooks**

```bash
lefthook install
cat .git/hooks/post-commit | grep lefthook
```

Expected: hook file references lefthook.

- [ ] **Step 4: Commit**

```bash
git add lefthook.yml .cspace/.gitignore
git commit -m "Re-index code corpus on commit/checkout/merge via lefthook"
```

---

### Task 7.4: CI integration test job

**Files:**
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Add integration job**

In the existing CI file, add a new job (sibling of `test:`):

```yaml
  integration-search:
    runs-on: ubuntu-latest
    services:
      qdrant:
        image: qdrant/qdrant:latest
        ports: ["6333:6333"]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.22"
      # Note: llama / reduce / hdbscan sidecars are left to a dedicated
      # follow-up; for now run only tests tagged `!embedding` that exercise
      # Qdrant + indexer boundaries.
      - run: go test -tags=integration_qdrant ./search/...
```

Integration tests that need the full sidecar stack will run locally via `make integration-test` (out of scope for this task).

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "Add integration-search CI job for Qdrant-backed tests"
```

---

### Task 7.5: End-to-end smoke test

**Files:**
- Create: `scripts/search-smoke.sh`

- [ ] **Step 1: Write the script**

```bash
#!/usr/bin/env bash
# Exercises the full search stack against the cspace repo itself.
# Assumes sidecars are reachable (run from inside a cspace container, or
# adjust env vars).
set -euo pipefail

make build

./bin/cspace-search index --corpus=code --quiet
./bin/cspace-search index --corpus=commits --quiet
./bin/cspace-search clusters --corpus=code

./bin/cspace-search query --corpus=code "routing from client to host" --json > /tmp/hits.json
jq '.results[] | {path,score}' /tmp/hits.json

# Assertion: the routing chain should return at least one hit in docker helpers
# and one in compose templates.
if ! jq -e '.results[].path | select(test("internal/docker/"))' /tmp/hits.json >/dev/null; then
  echo "FAIL: no hit under internal/docker/"
  exit 1
fi
if ! jq -e '.results[].path | select(test("docker-compose"))' /tmp/hits.json >/dev/null; then
  echo "FAIL: no hit under compose templates"
  exit 1
fi

echo "Smoke test passed."
```

```bash
chmod +x scripts/search-smoke.sh
```

- [ ] **Step 2: Run from inside a cspace instance**

```bash
docker exec cs-venus sh -c 'cd /workspace && bash scripts/search-smoke.sh'
```

Expected: "Smoke test passed." Adjust the path pattern assertions if the current code structure evolves.

- [ ] **Step 3: Commit**

```bash
git add scripts/search-smoke.sh
git commit -m "Add end-to-end smoke test for code search"
```

---

## Phase 8 — Plot script portability (optional polish)

### Task 8.1: Make the plot script corpus-agnostic

Today `scripts/plot-clusters.py` reads `.cspace/cluster-coords.tsv`. Generalize it to accept a corpus id so code-corpus clusters can also be visualized.

**Files:**
- Modify: `scripts/plot-clusters.py`

- [ ] **Step 1: Accept `--corpus` argument**

Change the default source path to `.cspace/{corpus}-cluster-coords.tsv`. Update the `cluster.Run` command to write to the same path. Adjust the plot title to include the corpus id.

- [ ] **Step 2: Regenerate plots for both corpora**

```bash
./bin/cspace-search clusters --corpus=commits --coords-out=.cspace/commits-cluster-coords.tsv
./bin/cspace-search clusters --corpus=code    --coords-out=.cspace/code-cluster-coords.tsv
python3 scripts/plot-clusters.py --corpus=commits
python3 scripts/plot-clusters.py --corpus=code
```

- [ ] **Step 3: Commit**

```bash
git add scripts/plot-clusters.py
git commit -m "Generalize plot-clusters.py to accept a corpus id"
```

---

## Spec coverage self-review

Each requirement from the spec is covered by at least one task:

| Spec section | Task(s) |
|---|---|
| Corpus abstraction | 1.2, 1.4 |
| CodeCorpus content scope + filter | 3.1 |
| Granularity (file + chunked) | 3.2, 3.3 |
| Freshness (manual + lefthook) | 4.2, 7.3 |
| Packaging (search/ outside internal/, CI guard, two binaries) | 1.1, 4.2, 5.1, 7.2 |
| Compose topology | 7.1 |
| CLI surface | 4.2, 6.1 |
| Result shape (envelope + Hit) | 2.4 |
| MCP tool surface | 5.1 |
| Error handling | 2.1 (lock), 2.2 (orphans), 2.4 (degenerate query) |
| Testing | 1.2, 1.4, 1.5, 2.1, 2.4, 2.5, 3.1, 3.2, 3.3, 4.1, 5.1, 7.5 |
| Qdrant payload widening | 1.3 |
| cluster_id write-back | 2.5 |
| Collection naming | 1.3 (ProjectHash), 1.4/3.3 (corpus prefix) |

No requirement is missed; no task references an undefined type or function.

---

## Execution handoff

Plan saved to `docs/superpowers/plans/2026-04-20-code-search.md`. User has pre-selected subagent-driven development in this PR; proceed to `superpowers:subagent-driven-development`.
