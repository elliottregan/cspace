package qdrant

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// QdrantClient is a minimal HTTP client for Qdrant's REST API.
type QdrantClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewQdrantClient returns a client with sensible defaults.
func NewQdrantClient(baseURL string) *QdrantClient {
	return &QdrantClient{
		BaseURL:    baseURL,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// QdrantPoint is a single point to upsert.
type QdrantPoint struct {
	ID      uint64         `json:"id"`
	Vector  []float32      `json:"vector"`
	Payload map[string]any `json:"payload"`
}

// EnsureCollection creates the collection if it does not exist.
func (c *QdrantClient) EnsureCollection(name string, dim int) error {
	resp, err := c.HTTPClient.Get(c.BaseURL + "/collections/" + name)
	if err != nil {
		return fmt.Errorf("qdrant unreachable at %s: %w", c.BaseURL, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}

	body, _ := json.Marshal(map[string]any{
		"vectors": map[string]any{
			"size":     dim,
			"distance": "Cosine",
		},
	})
	req, _ := http.NewRequest(http.MethodPut, c.BaseURL+"/collections/"+name, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("creating collection: %w", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		return fmt.Errorf("qdrant create collection returned %d", resp2.StatusCode)
	}
	return nil
}

// DropCollection deletes the collection (used on re-index).
func (c *QdrantClient) DropCollection(name string) error {
	req, _ := http.NewRequest(http.MethodDelete, c.BaseURL+"/collections/"+name, nil)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

// UpsertPoints upserts points in batches of batchSize.
func (c *QdrantClient) UpsertPoints(collection string, points []QdrantPoint, batchSize int, progress func(done, total int)) error {
	url := c.BaseURL + "/collections/" + collection + "/points"
	for i := 0; i < len(points); i += batchSize {
		end := i + batchSize
		if end > len(points) {
			end = len(points)
		}
		body, err := json.Marshal(map[string]any{"points": points[i:end]})
		if err != nil {
			return err
		}
		req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return fmt.Errorf("upserting batch: %w", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("qdrant upsert returned %d", resp.StatusCode)
		}
		if progress != nil {
			progress(end, len(points))
		}
	}
	return nil
}

// ExistingPoints returns a map of point ID → content_hash for a collection,
// paginating through scroll. Used by the indexer for change detection.
func (c *QdrantClient) ExistingPoints(collection string) (map[uint64]string, error) {
	out := map[uint64]string{}
	var offset any = nil
	for {
		body := map[string]any{
			"limit":        256,
			"with_payload": []string{"content_hash"},
			"with_vector":  false,
		}
		if offset != nil {
			body["offset"] = offset
		}
		buf, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, c.BaseURL+"/collections/"+collection+"/points/scroll", bytes.NewReader(buf))
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("scroll: %w", err)
		}
		// Treat "collection missing" as empty so the indexer can create it.
		if resp.StatusCode == http.StatusNotFound {
			_ = resp.Body.Close()
			return map[uint64]string{}, nil
		}
		var parsed struct {
			Result struct {
				Points []struct {
					ID      uint64         `json:"id"`
					Payload map[string]any `json:"payload"`
				} `json:"points"`
				NextPageOffset any `json:"next_page_offset"`
			} `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
			_ = resp.Body.Close()
			return nil, err
		}
		_ = resp.Body.Close()
		for _, p := range parsed.Result.Points {
			if h, ok := p.Payload["content_hash"].(string); ok {
				out[p.ID] = h
			}
		}
		if parsed.Result.NextPageOffset == nil {
			break
		}
		offset = parsed.Result.NextPageOffset
	}
	return out, nil
}

// DeletePoints removes the listed point IDs.
func (c *QdrantClient) DeletePoints(collection string, ids []uint64) error {
	body, _ := json.Marshal(map[string]any{"points": ids})
	req, _ := http.NewRequest(http.MethodPost, c.BaseURL+"/collections/"+collection+"/points/delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("qdrant delete returned %d", resp.StatusCode)
	}
	return nil
}

// ScrolledPoint is a single point returned by ScrollPoints.
type ScrolledPoint struct {
	Vector  []float32
	Hash    string
	Date    string
	Subject string
}

type qdrantScrollResponse struct {
	Result struct {
		Points []struct {
			ID      uint64         `json:"id"`
			Vector  []float32      `json:"vector"`
			Payload map[string]any `json:"payload"`
		} `json:"points"`
		NextPageOffset *uint64 `json:"next_page_offset"`
	} `json:"result"`
}

// payloadString extracts a string from a map[string]any payload, returning ""
// if the key is absent or the value is not a string.
func payloadString(p map[string]any, key string) string {
	v, ok := p[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// ScrollPoints returns every point in the collection, including vectors.
func (c *QdrantClient) ScrollPoints(collection string) ([]ScrolledPoint, error) {
	url := c.BaseURL + "/collections/" + collection + "/points/scroll"
	var offset *uint64
	var all []ScrolledPoint
	for {
		req := map[string]any{
			"limit":        512,
			"with_payload": true,
			"with_vector":  true,
		}
		if offset != nil {
			req["offset"] = *offset
		}
		body, _ := json.Marshal(req)
		resp, err := c.HTTPClient.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("scroll: %w", err)
		}
		var sr qdrantScrollResponse
		if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
			_ = resp.Body.Close()
			return nil, err
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("qdrant scroll returned %d", resp.StatusCode)
		}
		for _, p := range sr.Result.Points {
			all = append(all, ScrolledPoint{
				Vector:  p.Vector,
				Hash:    payloadString(p.Payload, "hash"),
				Date:    payloadString(p.Payload, "date"),
				Subject: payloadString(p.Payload, "subject"),
			})
		}
		if sr.Result.NextPageOffset == nil {
			break
		}
		offset = sr.Result.NextPageOffset
	}
	return all, nil
}

// SearchResult is a single ANN hit.
type SearchResult struct {
	ID      uint64
	Score   float32
	Payload map[string]any
}

// SearchPoints performs an ANN query against a collection.
func (c *QdrantClient) SearchPoints(collection string, vector []float32, topK int) ([]SearchResult, error) {
	body, _ := json.Marshal(map[string]any{
		"vector":       vector,
		"limit":        topK,
		"with_payload": true,
	})
	req, _ := http.NewRequest(http.MethodPost, c.BaseURL+"/collections/"+collection+"/points/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qdrant search returned %d", resp.StatusCode)
	}
	var parsed struct {
		Result []struct {
			ID      uint64         `json:"id"`
			Score   float32        `json:"score"`
			Payload map[string]any `json:"payload"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	out := make([]SearchResult, len(parsed.Result))
	for i, r := range parsed.Result {
		out[i] = SearchResult{ID: r.ID, Score: r.Score, Payload: r.Payload}
	}
	return out, nil
}

// Result is a ranked item returned by a search query.
type Result struct {
	Score   float32
	Hash    string
	Date    string
	Subject string
}

type qdrantQueryResponse struct {
	Result struct {
		Points []struct {
			Score   float32        `json:"score"`
			Payload map[string]any `json:"payload"`
		} `json:"points"`
	} `json:"result"`
}

// QueryPoints returns the top-N most similar commits to queryVec.
func (c *QdrantClient) QueryPoints(collection string, queryVec []float32, topN int) ([]Result, error) {
	body, _ := json.Marshal(map[string]any{
		"query":        queryVec,
		"limit":        topN,
		"with_payload": true,
	})
	resp, err := c.HTTPClient.Post(
		c.BaseURL+"/collections/"+collection+"/points/query",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("qdrant query: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qdrant query returned %d", resp.StatusCode)
	}

	var qr qdrantQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&qr); err != nil {
		return nil, fmt.Errorf("decoding qdrant response: %w", err)
	}

	results := make([]Result, len(qr.Result.Points))
	for i, p := range qr.Result.Points {
		results[i] = Result{
			Score:   p.Score,
			Hash:    payloadString(p.Payload, "hash"),
			Date:    payloadString(p.Payload, "date"),
			Subject: payloadString(p.Payload, "subject"),
		}
	}
	return results, nil
}
