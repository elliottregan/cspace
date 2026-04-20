package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ReduceClient calls the reduce-api sidecar (PaCMAP / LocalMAP).
type ReduceClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewReduceClient returns a client with sensible defaults.
func NewReduceClient(baseURL string) *ReduceClient {
	return &ReduceClient{
		BaseURL:    baseURL,
		HTTPClient: &http.Client{Timeout: 10 * time.Minute},
	}
}

type reduceRequest struct {
	Reducer     string  `json:"reducer"`
	Distance    string  `json:"distance"`
	Dimension   int     `json:"dimension"`
	NNeighbors  int     `json:"n_neighbors"`
	ApplyPCA    bool    `json:"apply_pca"`
	Seed        int     `json:"seed"`
	Content     string  `json:"content"`
}

type reduceResponse struct {
	Embedding [][]float32 `json:"embedding"`
}

// Reduce projects high-dim vectors to n_components dimensions (2 by default).
func (c *ReduceClient) Reduce(vectors [][]float32, nComponents int) ([][]float32, error) {
	content := vectorsToCSV(vectors)
	body, _ := json.Marshal(reduceRequest{
		Reducer:    "localmap",
		Distance:   "angular", // cosine-space embeddings → angular distance
		Dimension:  nComponents,
		NNeighbors: 10,
		ApplyPCA:   true,
		Seed:       21,
		Content:    content,
	})
	resp, err := c.HTTPClient.Post(c.BaseURL+"/reduce", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("reduce-api unreachable at %s: %w", c.BaseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("reduce-api returned %d", resp.StatusCode)
	}
	var rr reduceResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return nil, err
	}
	return rr.Embedding, nil
}

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

// vectorsToCSV serializes a slice of float32 vectors into the CSV string
// format expected by reduce-api.
func vectorsToCSV(vectors [][]float32) string {
	var sb strings.Builder
	for _, v := range vectors {
		for i, x := range v {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(strconv.FormatFloat(float64(x), 'g', 6, 32))
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// Cluster is a group of related commits discovered by the reduce+cluster pipeline.
type Cluster struct {
	ID       int
	Density  float32 // mean cosine similarity of members in original high-dim space
	Members  []ScrolledPoint
	Coords2D [][]float32
}

// BuildClusters converts per-point labels into ranked Cluster objects.
// Members within each cluster are ordered by centrality (mean similarity to
// other members in the original high-dim space). Clusters are ranked by
// size × density (the hotspot score).
func BuildClusters(points []ScrolledPoint, coords [][]float32, labels []int) (clusters []Cluster, noise int) {
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

		sortedPts := make([]ScrolledPoint, len(members))
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
