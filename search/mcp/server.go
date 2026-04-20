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

// Server holds runtime dependencies for search_code / list_clusters tools.
type Server struct {
	ProjectRoot string
	Config      *config.Config
}

// Register attaches both tools to the provided MCP server.
func (s *Server) Register(srv *mcpSDK.Server) {
	mcpSDK.AddTool[searchCodeInput, searchCodeOutput](srv, &mcpSDK.Tool{
		Name:        "search_code",
		Description: "Semantic search over the indexed code corpus. Returns ranked file hits with path, line range, score, and optional cluster ID.",
	}, func(ctx context.Context, req *mcpSDK.CallToolRequest, in searchCodeInput) (*mcpSDK.CallToolResult, searchCodeOutput, error) {
		return s.handleSearchCode(ctx, in)
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
	Query       string `json:"query"        jsonschema:"the natural language query to embed and search"`
	TopK        int    `json:"top_k,omitempty" jsonschema:"max results to return (default 10, max 50)"`
	WithCluster bool   `json:"with_cluster,omitempty" jsonschema:"include cluster_id per hit"`
}

type searchCodeOutput struct {
	Results json.RawMessage `json:"results"`
}

type listClustersInput struct{}

type listClustersOutput struct {
	Clusters json.RawMessage `json:"clusters"`
}

// --- Handlers ---

func (s *Server) handleSearchCode(ctx context.Context, in searchCodeInput) (*mcpSDK.CallToolResult, searchCodeOutput, error) {
	rt, err := config.BuildWithConfig(s.ProjectRoot, "code", s.Config)
	if err != nil {
		return nil, searchCodeOutput{}, err
	}
	qc := qdrant.NewQdrantClient(s.Config.Sidecars.QdrantURL)
	ec := embed.NewClient(s.Config.Sidecars.LlamaRetrievalURL)

	topK := in.TopK
	if topK <= 0 {
		topK = 10
	}

	env, err := query.Run(ctx, query.Config{
		Corpus:      rt.Corpus,
		Embedder:    &embed.QueryAdapter{Client: ec},
		Searcher:    &qdrant.Adapter{QdrantClient: qc},
		ProjectRoot: s.ProjectRoot,
		Query:       in.Query,
		TopK:        topK,
		WithCluster: in.WithCluster,
	})
	if err != nil {
		return nil, searchCodeOutput{}, err
	}

	buf, err := json.Marshal(env)
	if err != nil {
		return nil, searchCodeOutput{}, err
	}
	out := searchCodeOutput{Results: json.RawMessage(buf)}
	return &mcpSDK.CallToolResult{
		Content: []mcpSDK.Content{&mcpSDK.TextContent{Text: string(buf)}},
	}, out, nil
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
