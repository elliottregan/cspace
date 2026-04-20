package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"time"
)

// ClusterClient calls the dim-reduce sidecar (UMAP → HDBSCAN in Python).
type ClusterClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewClusterClient returns a client with sensible defaults.
func NewClusterClient(baseURL string) *ClusterClient {
	return &ClusterClient{
		BaseURL:    baseURL,
		HTTPClient: &http.Client{Timeout: 10 * time.Minute},
	}
}

type clusterRequest struct {
	Vectors        [][]float32 `json:"vectors"`
	MinClusterSize int         `json:"min_cluster_size"`
	MinSamples     int         `json:"min_samples"`
}

type clusterResponse struct {
	Labels []int       `json:"labels"`
	Coords [][]float32 `json:"coords"`
}

// Cluster sends vectors to the sidecar; returns per-point labels (-1 = noise)
// and 2D UMAP coordinates (useful for plotting).
func (c *ClusterClient) Cluster(vectors [][]float32, minClusterSize, minSamples int) (labels []int, coords [][]float32, err error) {
	body, _ := json.Marshal(clusterRequest{
		Vectors:        vectors,
		MinClusterSize: minClusterSize,
		MinSamples:     minSamples,
	})
	resp, err := c.HTTPClient.Post(c.BaseURL+"/cluster", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("dim-reduce unreachable at %s: %w", c.BaseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("dim-reduce returned %d", resp.StatusCode)
	}
	var cr clusterResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, nil, err
	}
	return cr.Labels, cr.Coords, nil
}

// Cluster is a group of related commits discovered via UMAP + HDBSCAN.
type Cluster struct {
	ID       int
	Density  float32 // mean cosine similarity of members in original 768D space
	Members  []ScrolledPoint
	Coords2D [][]float32 // 2D UMAP coordinates per member
}

// BuildClusters converts per-point labels into ranked Cluster objects.
// Members within each cluster are ordered by centrality (mean similarity
// to other members in the original 768D space). Clusters are ranked by
// size × density (the "hotspot" score).
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
		// Density = mean pairwise cosine similarity in original 768D space.
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
