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
	mcpSDK.AddTool[searchCodeInput, searchCodeOutput](srv, &mcpSDK.Tool{
		Name:        "search_code",
		Description: "Semantic search over the indexed code corpus. Returns ranked file hits with path, line range, score, and optional cluster ID.",
	}, func(ctx context.Context, req *mcpSDK.CallToolRequest, in searchCodeInput) (*mcpSDK.CallToolResult, searchCodeOutput, error) {
		return s.handleSearchCode(ctx, in)
	})

	mcpSDK.AddTool[searchContextInput, searchContextOutput](srv, &mcpSDK.Tool{
		Name:        "search_context",
		Description: "Semantic search over the project's context artifacts (direction, principles, roadmap, findings, decisions, discoveries). Use when you need to check prior decisions, open findings, or stated architectural principles before acting.",
	}, func(ctx context.Context, req *mcpSDK.CallToolRequest, in searchContextInput) (*mcpSDK.CallToolResult, searchContextOutput, error) {
		return s.handleSearchContext(ctx, in)
	})

	mcpSDK.AddTool[searchIssuesInput, searchIssuesOutput](srv, &mcpSDK.Tool{
		Name:        "search_issues",
		Description: "Semantic search over the repo's GitHub issues and PRs. Use to find prior discussion of a bug, feature request, or decision before proposing new work.",
	}, func(ctx context.Context, req *mcpSDK.CallToolRequest, in searchIssuesInput) (*mcpSDK.CallToolResult, searchIssuesOutput, error) {
		return s.handleSearchIssues(ctx, in)
	})

	mcpSDK.AddTool[listClustersInput, listClustersOutput](srv, &mcpSDK.Tool{
		Name:        "list_clusters",
		Description: "List the thematic clusters discovered in the code index. Returns cluster IDs, sizes, and representative file paths.",
	}, func(ctx context.Context, req *mcpSDK.CallToolRequest, in listClustersInput) (*mcpSDK.CallToolResult, listClustersOutput, error) {
		return s.handleListClusters(ctx)
	})
}

// --- Input / output types ---

type searchCodeInput struct {
	Query       string `json:"query"                  jsonschema:"the natural language query to embed and search"`
	TopK        int    `json:"top_k,omitempty"        jsonschema:"max results to return (default 10, max 50)"`
	WithCluster bool   `json:"with_cluster,omitempty" jsonschema:"include cluster_id per hit"`
}

type searchCodeOutput struct {
	Results json.RawMessage `json:"results"`
}

type searchContextInput struct {
	Query       string `json:"query"                  jsonschema:"the natural language query to embed and search against context artifacts"`
	TopK        int    `json:"top_k,omitempty"        jsonschema:"max results to return (default 10, max 50)"`
	WithCluster bool   `json:"with_cluster,omitempty" jsonschema:"include cluster_id per hit"`
}

type searchContextOutput struct {
	Results json.RawMessage `json:"results"`
}

type searchIssuesInput struct {
	Query       string `json:"query"                  jsonschema:"the natural language query to embed and search against issues and PRs"`
	TopK        int    `json:"top_k,omitempty"        jsonschema:"max results to return (default 10, max 50)"`
	WithCluster bool   `json:"with_cluster,omitempty" jsonschema:"include cluster_id per hit"`
}

type searchIssuesOutput struct {
	Results json.RawMessage `json:"results"`
}

type listClustersInput struct{}

type listClustersOutput struct {
	Clusters json.RawMessage `json:"clusters"`
}

// --- Handlers ---

// runCorpusQuery is a small helper that drives query.Run for a given corpus
// and returns the envelope JSON both as a CallToolResult text content and
// as a raw message embedded in the typed output.
func (s *Server) runCorpusQuery(ctx context.Context, corpusID, q string, topK int, withCluster bool) (*mcpSDK.CallToolResult, json.RawMessage, error) {
	rt, err := config.BuildWithConfig(s.ProjectRoot, corpusID, s.Config)
	if err != nil {
		return nil, nil, err
	}
	qc := qdrant.NewQdrantClient(s.Config.Sidecars.QdrantURL)
	ec := embed.NewClient(s.Config.Sidecars.LlamaRetrievalURL)

	if topK <= 0 {
		topK = 10
	}

	env, err := query.Run(ctx, query.Config{
		Corpus:      rt.Corpus,
		Embedder:    &embed.QueryAdapter{Client: ec},
		Searcher:    &qdrant.Adapter{QdrantClient: qc},
		ProjectRoot: s.ProjectRoot,
		Query:       q,
		TopK:        topK,
		WithCluster: withCluster,
	})
	if err != nil {
		return nil, nil, err
	}
	buf, err := json.Marshal(env)
	if err != nil {
		return nil, nil, err
	}
	return &mcpSDK.CallToolResult{
		Content: []mcpSDK.Content{&mcpSDK.TextContent{Text: string(buf)}},
	}, json.RawMessage(buf), nil
}

func (s *Server) handleSearchCode(ctx context.Context, in searchCodeInput) (*mcpSDK.CallToolResult, searchCodeOutput, error) {
	res, raw, err := s.runCorpusQuery(ctx, "code", in.Query, in.TopK, in.WithCluster)
	if err != nil {
		return nil, searchCodeOutput{}, err
	}
	return res, searchCodeOutput{Results: raw}, nil
}

func (s *Server) handleSearchContext(ctx context.Context, in searchContextInput) (*mcpSDK.CallToolResult, searchContextOutput, error) {
	res, raw, err := s.runCorpusQuery(ctx, "context", in.Query, in.TopK, in.WithCluster)
	if err != nil {
		return nil, searchContextOutput{}, err
	}
	return res, searchContextOutput{Results: raw}, nil
}

func (s *Server) handleSearchIssues(ctx context.Context, in searchIssuesInput) (*mcpSDK.CallToolResult, searchIssuesOutput, error) {
	res, raw, err := s.runCorpusQuery(ctx, "issues", in.Query, in.TopK, in.WithCluster)
	if err != nil {
		return nil, searchIssuesOutput{}, err
	}
	return res, searchIssuesOutput{Results: raw}, nil
}

func (s *Server) handleListClusters(ctx context.Context) (*mcpSDK.CallToolResult, listClustersOutput, error) {
	rt, err := config.BuildWithConfig(s.ProjectRoot, "code", s.Config)
	if err != nil {
		return nil, listClustersOutput{}, err
	}
	res, err := cluster.List(ctx, cluster.Config{
		Corpus:      rt.Corpus,
		ProjectRoot: s.ProjectRoot,
		QdrantURL:   s.Config.Sidecars.QdrantURL,
	})
	if err != nil {
		return nil, listClustersOutput{}, err
	}

	buf, err := json.Marshal(res)
	if err != nil {
		return nil, listClustersOutput{}, err
	}
	out := listClustersOutput{Clusters: json.RawMessage(buf)}
	return &mcpSDK.CallToolResult{
		Content: []mcpSDK.Content{&mcpSDK.TextContent{Text: string(buf)}},
	}, out, nil
}
