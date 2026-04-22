// Package mcp exposes search tools via the Model Context Protocol.
package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/elliottregan/cspace/search/cluster"
	"github.com/elliottregan/cspace/search/config"
	"github.com/elliottregan/cspace/search/corpus"
	"github.com/elliottregan/cspace/search/embed"
	"github.com/elliottregan/cspace/search/qdrant"
	"github.com/elliottregan/cspace/search/query"
	"github.com/elliottregan/cspace/search/status"

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

	mcpSDK.AddTool[statusInput, StatusOutput](srv, &mcpSDK.Tool{
		Name:        "search_status",
		Description: "Show the index status and staleness for all search corpora. Returns per-corpus state (completed/failed/disabled/unknown), whether the index is stale, and any in-progress indexing run. Use before high-stakes queries to decide whether to reindex first.",
	}, func(ctx context.Context, req *mcpSDK.CallToolRequest, in statusInput) (*mcpSDK.CallToolResult, StatusOutput, error) {
		return s.handleStatus(ctx)
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

type statusInput struct{}

// StatusOutput is the structured output from the search_status MCP tool.
type StatusOutput struct {
	Corpora map[string]CorpusStatusEntry `json:"corpora"`
	Current *status.RunningState         `json:"current"`
}

// CorpusStatusEntry describes one corpus's index state.
type CorpusStatusEntry struct {
	State        string `json:"state"`
	FinishedAt   string `json:"finished_at,omitempty"`
	DurationMS   int64  `json:"duration_ms,omitempty"`
	IndexedCount int    `json:"indexed_count,omitempty"`
	Error        string `json:"error,omitempty"`
	Stale        bool   `json:"stale,omitempty"`
	StaleReason  string `json:"stale_reason,omitempty"`
}

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

	// Annotate with staleness warning if applicable.
	mcpAppendStalenessWarning(env, corpusID, s.ProjectRoot, s.Config)

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

func (s *Server) handleStatus(_ context.Context) (*mcpSDK.CallToolResult, StatusOutput, error) {
	sf, err := status.Read(s.ProjectRoot)
	if err != nil {
		return nil, StatusOutput{}, err
	}

	allCorpora := []string{"code", "commits", "context", "issues"}
	out := StatusOutput{Corpora: make(map[string]CorpusStatusEntry)}
	if sf != nil {
		out.Current = sf.Current
	}

	for _, id := range allCorpora {
		entry := CorpusStatusEntry{State: "unknown"}

		// Check if disabled in config.
		if cc, ok := s.Config.Corpora[id]; ok && !cc.Enabled {
			entry.State = "disabled"
			out.Corpora[id] = entry
			continue
		}

		// Pull state from status file.
		if sf != nil {
			if cs, ok := sf.Last[id]; ok {
				entry.State = cs.State
				if !cs.FinishedAt.IsZero() {
					entry.FinishedAt = cs.FinishedAt.Format(time.RFC3339)
				}
				entry.DurationMS = cs.DurationMS
				entry.IndexedCount = cs.IndexedCount
				entry.Error = cs.Error
			}
		}

		// Check staleness for code and commits (cached to avoid per-query I/O).
		if entry.State == "completed" && (id == "code" || id == "commits") {
			qc := qdrant.NewQdrantClient(s.Config.Sidecars.QdrantURL)
			adapter := &qdrant.Adapter{QdrantClient: qc}
			collection := mcpCorpusCollection(id, s.ProjectRoot)
			var st corpus.Staleness
			switch id {
			case "code":
				st, _ = corpus.CodeStalenessCached(s.ProjectRoot, collection, adapter)
			case "commits":
				st, _ = corpus.CommitsStalenessCached(s.ProjectRoot, collection, adapter)
			}
			if st.IsStale {
				entry.Stale = true
				entry.StaleReason = st.Reason
			}
		}

		out.Corpora[id] = entry
	}

	buf, err := json.Marshal(out)
	if err != nil {
		return nil, StatusOutput{}, err
	}
	return &mcpSDK.CallToolResult{
		Content: []mcpSDK.Content{&mcpSDK.TextContent{Text: string(buf)}},
	}, out, nil
}

// mcpCorpusCollection returns the qdrant collection name for a corpus.
func mcpCorpusCollection(corpusID, projectRoot string) string {
	switch corpusID {
	case "code":
		return "code-" + corpus.ProjectHash(projectRoot)
	case "commits":
		return "commits-" + corpus.ProjectHash(projectRoot)
	default:
		return ""
	}
}

// mcpAppendStalenessWarning checks corpus staleness and appends a warning to
// the envelope. Best-effort: errors are silently ignored.
func mcpAppendStalenessWarning(env *query.Envelope, corpusID, projectRoot string, cfg *config.Config) {
	if corpusID != "code" && corpusID != "commits" {
		return
	}
	qc := qdrant.NewQdrantClient(cfg.Sidecars.QdrantURL)
	adapter := &qdrant.Adapter{QdrantClient: qc}
	collection := mcpCorpusCollection(corpusID, projectRoot)
	if collection == "" {
		return
	}
	var st corpus.Staleness
	var err error
	switch corpusID {
	case "code":
		st, err = corpus.CodeStalenessCached(projectRoot, collection, adapter)
	case "commits":
		st, err = corpus.CommitsStalenessCached(projectRoot, collection, adapter)
	}
	if err != nil || !st.IsStale {
		return
	}
	warning := "index may be out of date: " + st.Reason +
		" \u2014 run `cspace search " + corpusID + " index` to refresh"
	env.Warning = strings.TrimSpace(env.Warning + "\n" + warning)
}
