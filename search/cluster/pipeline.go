package cluster

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/elliottregan/cspace/search/qdrant"
	"github.com/elliottregan/cspace/search/reduce"
)

// Run reduces per-path representatives to 2D, clusters them with HDBSCAN,
// and writes cluster_id back onto every Qdrant point whose path maps to a
// clustered representative. Returns a ranked Result summary.
//
// Callers should invoke this when the underlying index has changed (after
// re-index). For a read-only view of already-stored cluster_ids, use List.
func Run(ctx context.Context, cfg Config) (*Result, error) {
	if cfg.MinPts == 0 {
		cfg.MinPts = 3
	}
	if cfg.MinSamples == 0 {
		cfg.MinSamples = 1
	}
	qc := qdrant.NewQdrantClient(cfg.QdrantURL)
	collection := cfg.Corpus.Collection(cfg.ProjectRoot)

	all, err := qc.ScrollAll(collection)
	if err != nil {
		return nil, fmt.Errorf("scroll: %w", err)
	}
	if len(all) == 0 {
		return &Result{}, nil
	}

	reps := collectRepresentatives(all)
	if len(reps) < cfg.MinPts {
		return &Result{}, nil
	}

	coords, err := reduceTo2D(cfg.ReduceURL, reps)
	if err != nil {
		return nil, err
	}

	labels, err := runHDBSCAN(cfg.HDBSCANURL, coords, cfg.MinPts, cfg.MinSamples)
	if err != nil {
		return nil, err
	}
	if len(labels) != len(reps) {
		return nil, fmt.Errorf("label count mismatch: %d vs %d", len(labels), len(reps))
	}

	if err := writeBackClusterIDs(qc, collection, all, reps, labels); err != nil {
		return nil, err
	}

	if cfg.CoordsOutput != "" {
		if err := writeCoordsTSV(cfg.CoordsOutput, reps, coords, labels); err != nil {
			return nil, fmt.Errorf("write coords: %w", err)
		}
	}

	return summarize(reps, labels), nil
}

// collectRepresentatives extracts one representative per path from all
// scrolled points.
func collectRepresentatives(all []qdrant.FullPoint) []representative {
	reps := make([]representative, 0, len(all))
	for _, p := range all {
		path, _ := p.Payload["path"].(string)
		kind, _ := p.Payload["kind"].(string)
		ls := 0
		if v, ok := p.Payload["line_start"].(float64); ok {
			ls = int(v)
		}
		reps = append(reps, representative{
			ID: p.ID, Path: path, Kind: kind, LineStart: ls, Vector: p.Vector,
		})
	}
	return pickRepresentative(reps)
}

// reduceTo2D projects representative vectors to 2D via the reduce-api.
func reduceTo2D(reduceURL string, reps []representative) ([][]float32, error) {
	vectors := make([][]float32, len(reps))
	for i, r := range reps {
		vectors[i] = r.Vector
	}
	rc := reduce.NewReduceClient(reduceURL)
	coords, err := rc.Reduce(vectors, 2)
	if err != nil {
		return nil, fmt.Errorf("reduce: %w", err)
	}
	return coords, nil
}

// runHDBSCAN clusters 2D coords via the hdbscan-api.
func runHDBSCAN(hdbscanURL string, coords [][]float32, minPts, minSamples int) ([]int, error) {
	hc := NewHdbscanClient(hdbscanURL)
	labels, err := hc.Cluster(coords, minPts, minSamples)
	if err != nil {
		return nil, fmt.Errorf("hdbscan: %w", err)
	}
	return labels, nil
}

// writeBackClusterIDs batches set_payload calls, one per cluster, updating
// every Qdrant point whose path maps to a clustered representative.
func writeBackClusterIDs(qc *qdrant.QdrantClient, collection string, all []qdrant.FullPoint, reps []representative, labels []int) error {
	pathToCluster := make(map[string]int, len(reps))
	for i, r := range reps {
		pathToCluster[r.Path] = labels[i]
	}
	byCluster := map[int][]uint64{}
	for _, p := range all {
		path, _ := p.Payload["path"].(string)
		if cid, ok := pathToCluster[path]; ok {
			byCluster[cid] = append(byCluster[cid], p.ID)
		}
	}
	for cid, ids := range byCluster {
		if err := qc.SetPayload(collection, ids, map[string]any{"cluster_id": cid}); err != nil {
			return fmt.Errorf("set cluster_id: %w", err)
		}
	}
	return nil
}

// summarize builds a ranked Result from the representatives and their
// HDBSCAN labels.
func summarize(reps []representative, labels []int) *Result {
	type acc struct {
		size  int
		paths []string
	}
	m := map[int]*acc{}
	for i, r := range reps {
		if labels[i] < 0 {
			continue
		}
		a := m[labels[i]]
		if a == nil {
			a = &acc{}
			m[labels[i]] = a
		}
		a.size++
		if len(a.paths) < 6 {
			a.paths = append(a.paths, r.Path)
		}
	}
	out := make([]ClusterSummary, 0, len(m))
	for cid, a := range m {
		out = append(out, ClusterSummary{ClusterID: cid, Size: a.size, TopPaths: a.paths})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Size > out[j].Size })
	return &Result{Clusters: out}
}

// writeCoordsTSV writes the TSV consumed by scripts/plot-clusters.py.
func writeCoordsTSV(path string, reps []representative, coords [][]float32, labels []int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintln(f, "id\tpath\tx\ty\tlabel")
	for i, r := range reps {
		_, _ = fmt.Fprintf(f, "%d\t%s\t%f\t%f\t%d\n", r.ID, r.Path, coords[i][0], coords[i][1], labels[i])
	}
	return nil
}
