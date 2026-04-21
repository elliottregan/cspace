package cluster

import (
	"context"
	"sort"

	"github.com/elliottregan/cspace/search/qdrant"
)

// List returns the current cluster summary by scrolling Qdrant payloads.
// It does not re-run the reduce/HDBSCAN pipeline — it only reads the
// cluster_id values most recently written by Run.
//
// Points with cluster_id < 0 (noise, or pre-clustering) are skipped.
func List(ctx context.Context, cfg Config) (*Result, error) {
	qc := qdrant.NewQdrantClient(cfg.QdrantURL)
	all, err := qc.ScrollAll(cfg.Corpus.Collection(cfg.ProjectRoot))
	if err != nil {
		return nil, err
	}

	// cluster_id → set of unique paths in that cluster.
	clusters := map[int]map[string]bool{}
	for _, p := range all {
		path, _ := p.Payload["path"].(string)
		cid := -1
		if v, ok := p.Payload["cluster_id"].(float64); ok {
			cid = int(v)
		}
		if cid < 0 {
			continue
		}
		if clusters[cid] == nil {
			clusters[cid] = map[string]bool{}
		}
		clusters[cid][path] = true
	}

	out := make([]ClusterSummary, 0, len(clusters))
	for cid, paths := range clusters {
		list := make([]string, 0, len(paths))
		for p := range paths {
			list = append(list, p)
		}
		sort.Strings(list)
		top := list
		if len(top) > 6 {
			top = top[:6]
		}
		out = append(out, ClusterSummary{ClusterID: cid, Size: len(list), TopPaths: top})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Size > out[j].Size })
	return &Result{Clusters: out}, nil
}
