// Package mcp exposes search tools via the Model Context Protocol.
package mcp

import (
	"context"
	"encoding/json"

	"github.com/elliottregan/cspace/search/cluster"
	"github.com/elliottregan/cspace/search/config"
	"github.com/elliottregan/cspace/search/embed"
	"github.com/elliottregan/cspace/search/qdrant"
	"github.com/elliottregan/cspace/search/query"

	mcpSDK "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server holds runtime dependencies for the MCP tool handlers.
type Server struct {
	ProjectRoot string
	Config      *config.Config
}

// Register attaches all search tools to the provided MCP server.
func (s *Server) Register(srv *mcpSDK.Server) {
	mcpSDK.AddTool[searchInput, query.Envelope](srv, &mcpSDK.Tool{
		Name:        "search_code",
		Description: "Semantic search over the indexed code corpus. Returns ranked file hits with path, line range, score, and optional cluster ID.",
	}, func(ctx context.Context, req *mcpSDK.CallToolRequest, in searchInput) (*mcpSDK.CallToolResult, query.Envelope, error) {
		return s.handleSearch(ctx, "code", in)
	})

	mcpSDK.AddTool[searchInput, query.Envelope](srv, &mcpSDK.Tool{
		Name:        "search_context",
		Description: "Semantic search over the project's context artifacts (direction, principles, roadmap, findings, decisions, discoveries). Use when you need to check prior decisions, open findings, or stated architectural principles before acting.",
	}, func(ctx context.Context, req *mcpSDK.CallToolRequest, in searchInput) (*mcpSDK.CallToolResult, query.Envelope, error) {
		return s.handleSearch(ctx, "context", in)
	})

	mcpSDK.AddTool[searchInput, query.Envelope](srv, &mcpSDK.Tool{
		Name:        "search_issues",
		Description: "Semantic search over the repo's GitHub issues and PRs. Use to find prior discussion of a bug, feature request, or decision before proposing new work.",
	}, func(ctx context.Context, req *mcpSDK.CallToolRequest, in searchInput) (*mcpSDK.CallToolResult, query.Envelope, error) {
		return s.handleSearch(ctx, "issues", in)
	})

	mcpSDK.AddTool[listClustersInput, cluster.Result](srv, &mcpSDK.Tool{
		Name:        "list_clusters",
		Description: "List the thematic clusters discovered in the code index. Returns cluster IDs, sizes, and representative file paths.",
	}, func(ctx context.Context, req *mcpSDK.CallToolRequest, in listClustersInput) (*mcpSDK.CallToolResult, cluster.Result, error) {
		return s.handleListClusters(ctx)
	})
}

// --- Input types ---

// searchInput is the shared input shape for the three semantic-search tools.
// They differ only in which corpus they consult.
type searchInput struct {
	Query       string `json:"query"                  jsonschema:"the natural language query to embed and search"`
	TopK        int    `json:"top_k,omitempty"        jsonschema:"max results to return (default 10, max 50)"`
	WithCluster bool   `json:"with_cluster,omitempty" jsonschema:"include cluster_id per hit"`
}

type listClustersInput struct{}

// --- Handlers ---

// handleSearch drives query.Run for a given corpus and returns the envelope
// as both CallToolResult text content and the MCP structured output. The
// structured output IS query.Envelope — don't wrap it in another named field
// or the result shape comes out double-nested ({"results":{"results":[...]}}).
func (s *Server) handleSearch(ctx context.Context, corpusID string, in searchInput) (*mcpSDK.CallToolResult, query.Envelope, error) {
	rt, err := config.BuildWithConfig(s.ProjectRoot, corpusID, s.Config)
	if err != nil {
		return nil, query.Envelope{}, err
	}
	qc := qdrant.NewQdrantClient(s.Config.Sidecars.QdrantURL)
	ec := embed.NewClient(s.Config.Sidecars.LlamaRetrievalURL)

	if in.TopK <= 0 {
		in.TopK = 10
	}

	env, err := query.Run(ctx, query.Config{
		Corpus:      rt.Corpus,
		Embedder:    &embed.QueryAdapter{Client: ec},
		Searcher:    &qdrant.Adapter{QdrantClient: qc},
		ProjectRoot: s.ProjectRoot,
		Query:       in.Query,
		TopK:        in.TopK,
		WithCluster: in.WithCluster,
	})
	if err != nil {
		return nil, query.Envelope{}, err
	}
	buf, err := json.Marshal(env)
	if err != nil {
		return nil, query.Envelope{}, err
	}
	return &mcpSDK.CallToolResult{
		Content: []mcpSDK.Content{&mcpSDK.TextContent{Text: string(buf)}},
	}, *env, nil
}

func (s *Server) handleListClusters(ctx context.Context) (*mcpSDK.CallToolResult, cluster.Result, error) {
	rt, err := config.BuildWithConfig(s.ProjectRoot, "code", s.Config)
	if err != nil {
		return nil, cluster.Result{}, err
	}
	res, err := cluster.List(ctx, cluster.Config{
		Corpus:      rt.Corpus,
		ProjectRoot: s.ProjectRoot,
		QdrantURL:   s.Config.Sidecars.QdrantURL,
	})
	if err != nil {
		return nil, cluster.Result{}, err
	}
	buf, err := json.Marshal(res)
	if err != nil {
		return nil, cluster.Result{}, err
	}
	return &mcpSDK.CallToolResult{
		Content: []mcpSDK.Content{&mcpSDK.TextContent{Text: string(buf)}},
	}, *res, nil
}
