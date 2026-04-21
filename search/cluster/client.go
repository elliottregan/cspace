package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
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

// Cluster runs HDBSCAN on the given points and returns per-point labels
// (-1 = noise, 0..k-1 = cluster ID).
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
