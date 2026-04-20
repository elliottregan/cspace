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
	ContentHash string  `json:"content_hash,omitempty"`
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
