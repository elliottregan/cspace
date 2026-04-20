package qdrant

import "github.com/elliottregan/cspace/search/index"

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

var _ index.Upserter = (*Adapter)(nil)
