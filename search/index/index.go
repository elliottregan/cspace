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
	Corpus      corpus.Corpus
	Embedder    Embedder
	Upserter    Upserter
	ProjectRoot string
	BatchSize   int // default 32
	Dim         int // default 768 (Jina v5 nano)
	LockPath    string
	Progress    func(done, total int)
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
			return err
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
		if h, ok := existing[id]; ok && r.ContentHash != "" && h == r.ContentHash {
			continue
		}
		toEmbed = append(toEmbed, r)
	}

	total := len(toEmbed)
	for i := 0; i < total; i += cfg.BatchSize {
		end := i + cfg.BatchSize
		if end > total {
			end = total
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
		if len(vecs) != len(batch) {
			return fmt.Errorf("embedder returned %d vectors for %d texts", len(vecs), len(batch))
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
