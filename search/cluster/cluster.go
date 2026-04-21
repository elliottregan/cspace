// Package cluster runs the reduce → HDBSCAN → write-back pipeline that
// turns a corpus's embeddings into a map of thematic zones.
//
// The package surface is intentionally small:
//
//   - Run(ctx, Config) rebuilds the clustering and writes cluster_id
//     back onto every Qdrant point whose path belongs to a clustered
//     representative. Expensive; call when the index has changed.
//   - List(ctx, Config) reads the currently-stored cluster_ids and
//     returns a ranked summary without re-running the pipeline.
//
// Types defined here (Config / Result / ClusterSummary) are the public
// contract. The HDBSCAN HTTP client, the per-path representative picker,
// and the pipeline wiring each live in their own sibling files.
package cluster

import "github.com/elliottregan/cspace/search/corpus"

// Config bundles the params for both Run and List. Not every field is
// used by every entry point: List ignores ReduceURL, HDBSCANURL, MinPts,
// MinSamples, and CoordsOutput.
type Config struct {
	Corpus       corpus.Corpus
	ProjectRoot  string
	QdrantURL    string
	ReduceURL    string
	HDBSCANURL   string
	CoordsOutput string // optional TSV output path (Run only)
	MinPts       int    // HDBSCAN min_cluster_size (minimum points to form a cluster)
	MinSamples   int    // HDBSCAN min_samples (cluster conservatism; higher = more noise, tighter clusters)
}

// Result summarizes a clustering pass.
type Result struct {
	Clusters []ClusterSummary `json:"clusters"`
}

// ClusterSummary describes one cluster.
type ClusterSummary struct {
	ClusterID int      `json:"cluster_id"`
	Size      int      `json:"size"`
	TopPaths  []string `json:"top_paths"`
}
