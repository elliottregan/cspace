package embed

import (
	"context"

	"github.com/elliottregan/cspace/search/index"
)

// Adapter wraps *Client to satisfy index.Embedder (batch embedding for the
// indexer). It applies the "Document: " prefix that Jina retrieval models
// expect on the document side.
type Adapter struct{ *Client }

// Embed implements index.Embedder by delegating to EmbedDocuments.
func (a *Adapter) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return a.Client.EmbedDocuments(ctx, texts, nil)
}

// QueryAdapter wraps *Client for single-query embedding. A later task (2.4)
// adds a query.Embedder interface this satisfies via EmbedQuery.
type QueryAdapter struct{ *Client }

// EmbedQuery embeds a single query string with the "Query: " retrieval prefix
// that Jina models expect on the query side.
func (a *QueryAdapter) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	return a.Client.EmbedQuery(ctx, text)
}

// Compile-time assertion: Adapter satisfies index.Embedder.
var _ index.Embedder = (*Adapter)(nil)
