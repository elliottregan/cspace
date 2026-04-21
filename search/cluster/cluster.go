package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/elliottregan/cspace/search/corpus"
	"github.com/elliottregan/cspace/search/qdrant"
	"github.com/elliottregan/cspace/search/reduce"
)

// HdbscanClient calls the hdbscan-api sidecar.
type HdbscanClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewHdbscanClient returns a client with sensible defaults.
func NewHdbscanClient(baseURL string) *HdbscanClient {
	return &HdbscanClient{
		BaseURL:    baseURL,
		HTTPClient: &http.Client{Timeout: 5 * time.Minute},
	}
}

type hdbscanRequest struct {
	Points         [][]float32 `json:"points"`
	MinClusterSize int         `json:"min_cluster_size"`
	MinSamples     int         `json:"min_samples"`
}

type hdbscanResponse struct {
	Labels []int `json:"labels"`
}

// Cluster runs HDBSCAN on the given 2D (or any-D) points and returns per-point
// labels (-1 = noise, 0..k-1 = cluster ID).
func (c *HdbscanClient) Cluster(points [][]float32, minClusterSize, minSamples int) ([]int, error) {
	body, _ := json.Marshal(hdbscanRequest{
		Points:         points,
		MinClusterSize: minClusterSize,
		MinSamples:     minSamples,
	})
	resp, err := c.HTTPClient.Post(c.BaseURL+"/cluster", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("hdbscan-api unreachable at %s: %w", c.BaseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hdbscan-api returned %d", resp.StatusCode)
	}
	var hr hdbscanResponse
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		return nil, err
	}
	return hr.Labels, nil
}

// Cluster is a group of related commits discovered by the reduce+cluster pipeline.
type Cluster struct {
	ID       int
	Density  float32 // mean cosine similarity of members in original high-dim space
	Members  []qdrant.ScrolledPoint
	Coords2D [][]float32
}

// BuildClusters converts per-point labels into ranked Cluster objects.
// Members within each cluster are ordered by centrality (mean similarity to
// other members in the original high-dim space). Clusters are ranked by
// size × density (the hotspot score).
func BuildClusters(points []qdrant.ScrolledPoint, coords [][]float32, labels []int) (clusters []Cluster, noise int) {
	groups := map[int][]int{}
	for i, l := range labels {
		if l < 0 {
			noise++
			continue
		}
		groups[l] = append(groups[l], i)
	}

	for id, idxs := range groups {
		var sum float32
		var pairs int
		for i := 0; i < len(idxs); i++ {
			for j := i + 1; j < len(idxs); j++ {
				sum += cosineSim(points[idxs[i]].Vector, points[idxs[j]].Vector)
				pairs++
			}
		}
		density := float32(0)
		if pairs > 0 {
			density = sum / float32(pairs)
		}

		type scored struct {
			idx   int
			score float32
		}
		members := make([]scored, len(idxs))
		for i, ii := range idxs {
			var s float32
			for _, jj := range idxs {
				if ii == jj {
					continue
				}
				s += cosineSim(points[ii].Vector, points[jj].Vector)
			}
			if len(idxs) > 1 {
				s /= float32(len(idxs) - 1)
			}
			members[i] = scored{ii, s}
		}
		sort.Slice(members, func(i, j int) bool {
			return members[i].score > members[j].score
		})

		sortedPts := make([]qdrant.ScrolledPoint, len(members))
		sortedCoords := make([][]float32, len(members))
		for i, m := range members {
			sortedPts[i] = points[m.idx]
			sortedCoords[i] = coords[m.idx]
		}
		clusters = append(clusters, Cluster{
			ID:       id,
			Density:  density,
			Members:  sortedPts,
			Coords2D: sortedCoords,
		})
	}

	sort.Slice(clusters, func(i, j int) bool {
		si := float32(len(clusters[i].Members)) * clusters[i].Density
		sj := float32(len(clusters[j].Members)) * clusters[j].Density
		return si > sj
	})
	for i := range clusters {
		clusters[i].ID = i + 1
	}
	return clusters, noise
}

func cosineSim(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, magA, magB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		magA += float64(a[i]) * float64(a[i])
		magB += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(magA) * math.Sqrt(magB)
	if denom == 0 {
		return 0
	}
	return float32(dot / denom)
}

// representative tracks a single candidate point for one path during
// cluster-representative selection.
type representative struct {
	ID        uint64
	Path      string
	Kind      string
	LineStart int
	Vector    []float32
}

// pickRepresentative returns one representative per path, preferring
// kind="file"; if none present, the chunk with the lowest LineStart wins.
func pickRepresentative(pts []representative) []representative {
	byPath := map[string][]representative{}
	for _, p := range pts {
		byPath[p.Path] = append(byPath[p.Path], p)
	}
	out := make([]representative, 0, len(byPath))
	for _, group := range byPath {
		var best *representative
		for i := range group {
			if best == nil {
				best = &group[i]
				continue
			}
			if best.Kind != "file" && group[i].Kind == "file" {
				best = &group[i]
				continue
			}
			if best.Kind == group[i].Kind && group[i].LineStart < best.LineStart {
				best = &group[i]
			}
		}
		out = append(out, *best)
	}
	return out
}

// Config bundles the params for the cluster pipeline.
type Config struct {
	Corpus       corpus.Corpus
	ProjectRoot  string
	QdrantURL    string
	ReduceURL    string
	HDBSCANURL   string
	CoordsOutput string // optional TSV output path
	MinPts       int    // HDBSCAN min_cluster_size (minimum points to form a cluster)
	MinSamples   int    // HDBSCAN min_samples (cluster conservatism; higher = more noise, tighter clusters)
}

// Result summarizes the clustering pass.
type Result struct {
	Clusters []ClusterSummary `json:"clusters"`
}

// ClusterSummary is one cluster's summary.
type ClusterSummary struct {
	ClusterID int      `json:"cluster_id"`
	Size      int      `json:"size"`
	TopPaths  []string `json:"top_paths"`
}

// Run reduces representatives to 2D, clusters them with HDBSCAN, and writes
// cluster_id back to ALL points in the collection whose path maps to a
// clustered representative. Returns a Result summary.
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

	// Build representatives (one per path).
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
	reps = pickRepresentative(reps)
	if len(reps) < cfg.MinPts {
		return &Result{}, nil
	}

	// Reduce to 2D.
	vectors := make([][]float32, len(reps))
	for i, r := range reps {
		vectors[i] = r.Vector
	}
	rc := reduce.NewReduceClient(cfg.ReduceURL)
	coords, err := rc.Reduce(vectors, 2)
	if err != nil {
		return nil, fmt.Errorf("reduce: %w", err)
	}

	// Cluster.
	hc := NewHdbscanClient(cfg.HDBSCANURL)
	labels, err := hc.Cluster(coords, cfg.MinPts, cfg.MinSamples)
	if err != nil {
		return nil, fmt.Errorf("hdbscan: %w", err)
	}
	if len(labels) != len(reps) {
		return nil, fmt.Errorf("label count mismatch: %d vs %d", len(labels), len(reps))
	}

	// Map path → cluster_id from representative labels.
	pathToCluster := map[string]int{}
	for i, r := range reps {
		pathToCluster[r.Path] = labels[i]
	}

	// Group all point IDs by cluster_id for batch set_payload.
	byCluster := map[int][]uint64{}
	for _, p := range all {
		path, _ := p.Payload["path"].(string)
		if cid, ok := pathToCluster[path]; ok {
			byCluster[cid] = append(byCluster[cid], p.ID)
		}
	}
	for cid, ids := range byCluster {
		if err := qc.SetPayload(collection, ids, map[string]any{"cluster_id": cid}); err != nil {
			return nil, fmt.Errorf("set cluster_id: %w", err)
		}
	}

	// Optional coords TSV output.
	if cfg.CoordsOutput != "" {
		if err := writeClusterCoordsTSV(cfg.CoordsOutput, reps, coords, labels); err != nil {
			return nil, fmt.Errorf("write coords: %w", err)
		}
	}

	return summarizeClusters(reps, labels), nil
}

// List returns the current cluster summary by scrolling Qdrant payloads.
// It does not re-run the pipeline.
func List(ctx context.Context, cfg Config) (*Result, error) {
	qc := qdrant.NewQdrantClient(cfg.QdrantURL)
	all, err := qc.ScrollAll(cfg.Corpus.Collection(cfg.ProjectRoot))
	if err != nil {
		return nil, err
	}
	// Map cluster_id → unique paths in that cluster.
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
	var out []ClusterSummary
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

func summarizeClusters(reps []representative, labels []int) *Result {
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

func writeClusterCoordsTSV(path string, reps []representative, coords [][]float32, labels []int) error {
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
