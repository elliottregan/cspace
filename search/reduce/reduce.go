package reduce

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
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
	Reducer    string `json:"reducer"`
	Distance   string `json:"distance"`
	Dimension  int    `json:"dimension"`
	NNeighbors int    `json:"n_neighbors"`
	ApplyPCA   bool   `json:"apply_pca"`
	Seed       int    `json:"seed"`
	Content    string `json:"content"`
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
