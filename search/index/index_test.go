package index

import (
	"context"
	"os"
	"testing"

	"github.com/elliottregan/cspace/search/corpus"
)

// fakeCorpus emits a handful of records for testing.
type fakeCorpus struct {
	records []corpus.Record
}

func (f *fakeCorpus) ID() string                 { return "fake" }
func (f *fakeCorpus) Collection(_ string) string { return "fake-test" }
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
	existing   map[uint64]string
	deleted    []uint64
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
	if u.existing == nil {
		return map[uint64]string{}, nil
	}
	return u.existing, nil
}
func (u *fakeUpserter) DeletePoints(_ string, ids []uint64) error {
	u.deleted = append(u.deleted, ids...)
	return nil
}

func TestRun_EmbedAndUpsertAllRecords(t *testing.T) {
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

func TestRun_SkipsUnchangedByContentHash(t *testing.T) {
	r1 := corpus.Record{Path: "a", Kind: "chunk", EmbedText: "alpha", ContentHash: "h1"}
	r2 := corpus.Record{Path: "b", Kind: "chunk", EmbedText: "bravo", ContentHash: "h2"}
	c := &fakeCorpus{records: []corpus.Record{r1, r2}}
	e := &fakeEmbedder{dim: 4}
	u := &fakeUpserter{
		existing: map[uint64]string{
			r1.ID(): "h1",  // unchanged
			r2.ID(): "old", // changed
		},
	}
	err := Run(context.Background(), Config{
		Corpus: c, Embedder: e, Upserter: u, ProjectRoot: ".",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(u.points) != 1 {
		t.Errorf("expected 1 point upserted (only the changed one), got %d", len(u.points))
	}
}

func TestRun_DeletesOrphans(t *testing.T) {
	r := corpus.Record{Path: "a", Kind: "chunk", EmbedText: "alpha", ContentHash: "h1"}
	c := &fakeCorpus{records: []corpus.Record{r}}
	orphanID := uint64(0xdeadbeef)
	u := &fakeUpserter{
		existing: map[uint64]string{
			r.ID():   "h1",
			orphanID: "whatever",
		},
	}
	err := Run(context.Background(), Config{
		Corpus: c, Embedder: &fakeEmbedder{dim: 4}, Upserter: u, ProjectRoot: ".",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(u.deleted) != 1 || u.deleted[0] != orphanID {
		t.Errorf("expected orphan %d to be deleted, got %v", orphanID, u.deleted)
	}
}

func TestRun_LockFilePreventsConcurrentRun(t *testing.T) {
	tmp := t.TempDir()
	lock := tmp + "/lock"
	// Pre-create the lock.
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("pre-create lock: %v", err)
	}
	_ = f.Close()
	defer func() { _ = os.Remove(lock) }()

	cfg := Config{
		Corpus:   &fakeCorpus{},
		Embedder: &fakeEmbedder{dim: 4},
		Upserter: &fakeUpserter{},
		LockPath: lock,
	}
	err = Run(context.Background(), cfg)
	if err == nil {
		t.Error("expected error when lock is held, got nil")
	}
}
