package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client calls a llama.cpp server's OpenAI-compatible /v1/embeddings endpoint.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewClient returns a Client with sensible defaults.
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

type embedRequest struct {
	Input []string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

const defaultBatchSize = 4

// Embed returns one embedding vector per input text, batching requests to avoid
// overwhelming the server's parallel slot limit.
func (c *Client) Embed(texts []string) ([][]float32, error) {
	return c.EmbedWithProgress(texts, nil)
}

// EmbedWithProgress embeds texts in batches, calling progress(done, total) after each batch.
func (c *Client) EmbedWithProgress(texts []string, progress func(done, total int)) ([][]float32, error) {
	all := make([][]float32, 0, len(texts))
	for i := 0; i < len(texts); i += defaultBatchSize {
		end := i + defaultBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := c.embedBatch(texts[i:end])
		if err != nil {
			return nil, err
		}
		all = append(all, vecs...)
		if progress != nil {
			progress(len(all), len(texts))
		}
	}
	return all, nil
}

func (c *Client) embedBatch(texts []string) ([][]float32, error) {
	body, err := json.Marshal(embedRequest{Input: texts})
	if err != nil {
		return nil, err
	}

	resp, err := c.HTTPClient.Post(c.BaseURL+"/v1/embeddings", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llama.cpp server unreachable at %s: %w\n"+
			"Start it with: llama-server -m <model>.gguf --embedding", c.BaseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llama.cpp server returned %d", resp.StatusCode)
	}

	var result embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding embedding response: %w", err)
	}

	vecs := make([][]float32, len(result.Data))
	for i, d := range result.Data {
		vecs[i] = d.Embedding
	}
	return vecs, nil
}
