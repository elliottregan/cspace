package qdrant

import (
	"github.com/elliottregan/cspace/search/index"
	"github.com/elliottregan/cspace/search/query"
)

// Adapter wraps *QdrantClient to satisfy index.Upserter.
type Adapter struct{ *QdrantClient }

// UpsertPoints converts index.Point to QdrantPoint and delegates to the
// underlying client.
func (a *Adapter) UpsertPoints(collection string, points []index.Point, batchSize int, progress func(int, int)) error {
	qp := make([]QdrantPoint, len(points))
	for i, p := range points {
		qp[i] = QdrantPoint{ID: p.ID, Vector: p.Vector, Payload: p.Payload}
	}
	return a.QdrantClient.UpsertPoints(collection, qp, batchSize, progress)
}

// Search satisfies query.Searcher.
func (a *Adapter) Search(collection string, vector []float32, topK int) ([]query.RawHit, error) {
	raws, err := a.SearchPoints(collection, vector, topK)
	if err != nil {
		return nil, err
	}
	out := make([]query.RawHit, len(raws))
	for i, r := range raws {
		out[i] = query.RawHit{ID: r.ID, Score: r.Score, Payload: r.Payload}
	}
	return out, nil
}

// MaxPayloadDate scrolls a collection and returns the maximum "date" payload
// field (format "2006-01-02"). Used by CommitsStaleness to compare HEAD against
// the latest indexed commit without false positives under commits.limit.
func (a *Adapter) MaxPayloadDate(collection string) (string, error) {
	return a.QdrantClient.MaxPayloadDate(collection)
}

var _ index.Upserter = (*Adapter)(nil)
var _ query.Searcher = (*Adapter)(nil)
