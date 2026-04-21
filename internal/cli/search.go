package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/elliottregan/cspace/search/cluster"
	"github.com/elliottregan/cspace/search/config"
	"github.com/elliottregan/cspace/search/embed"
	"github.com/elliottregan/cspace/search/index"
	searchmcp "github.com/elliottregan/cspace/search/mcp"
	"github.com/elliottregan/cspace/search/qdrant"
	"github.com/elliottregan/cspace/search/query"

	mcpSDK "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func newSearchCmd() *cobra.Command {
	var topK int
	cmd := &cobra.Command{
		Use:     "search",
		Short:   "Semantic search over commits and code",
		Long:    "Subcommands: code, commits. Back-compat: `cspace search \"<query>\"` runs a commits query.",
		GroupID: "other",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			// Back-compat: 1 positional arg runs commits query (old behaviour).
			return runSearchQuery("commits", args[0], searchQueryOpts{TopK: topK})
		},
	}
	cmd.Flags().IntVar(&topK, "top", 10, "Number of results to show (back-compat flag)")
	cmd.AddCommand(newSearchSubcmd("code"), newSearchSubcmd("commits"), newSearchMCPCmd())
	return cmd
}

// newSearchMCPCmd builds `cspace search mcp`, a stdio MCP server exposing
// search_code + list_clusters. Registered per agent container via
// init-claude-plugins.sh so advisors/coordinators/implementers can consult
// the index mid-session. Parallels `cspace context-server`.
func newSearchMCPCmd() *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the search MCP server over stdio",
		Long: `Expose the code + commits search indexes as MCP tools (search_code,
list_clusters). Invoked by Claude Code via .mcp.json or a container's Claude
MCP config, not by humans directly.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if root == "" {
				root = searchProjectRoot()
			}
			cfg, err := config.Load(root)
			if err != nil {
				return err
			}
			server := mcpSDK.NewServer(&mcpSDK.Implementation{Name: "cspace-search", Version: Version}, nil)
			(&searchmcp.Server{ProjectRoot: root, Config: cfg}).Register(server)
			return server.Run(cmd.Context(), &mcpSDK.StdioTransport{})
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "Project root (default: git toplevel of cwd)")
	return cmd
}

// newSearchSubcmd builds `cspace search {code|commits}` with nested
// query / index / clusters subcommands.
func newSearchSubcmd(corpusID string) *cobra.Command {
	root := &cobra.Command{
		Use:   corpusID,
		Short: "Search " + corpusID,
	}

	// query
	{
		var topK int
		var asJSON bool
		var withCluster bool
		q := &cobra.Command{
			Use:   "query <query>",
			Short: "Run a semantic query",
			Args:  cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runSearchQuery(corpusID, strings.Join(args, " "), searchQueryOpts{
					TopK:        topK,
					JSON:        asJSON,
					WithCluster: withCluster,
				})
			},
		}
		q.Flags().IntVar(&topK, "top", 10, "top K hits")
		q.Flags().BoolVar(&asJSON, "json", false, "emit JSON envelope")
		q.Flags().BoolVar(&withCluster, "with-cluster", false, "include cluster_id per hit")
		root.AddCommand(q)
	}

	// index
	{
		var quiet bool
		i := &cobra.Command{
			Use:   "index",
			Short: "Build or refresh the index",
			RunE: func(cmd *cobra.Command, args []string) error {
				return runSearchIndex(corpusID, quiet)
			},
		}
		i.Flags().BoolVar(&quiet, "quiet", false, "suppress progress output")
		root.AddCommand(i)
	}

	// clusters
	{
		var coordsOut string
		var minPts int
		c := &cobra.Command{
			Use:   "clusters",
			Short: "Discover clusters and write cluster_id to index",
			RunE: func(cmd *cobra.Command, args []string) error {
				return runSearchClusters(corpusID, coordsOut, minPts)
			},
		}
		c.Flags().StringVar(&coordsOut, "coords-out", "", "write TSV of coords+labels")
		c.Flags().IntVar(&minPts, "min-pts", 3, "HDBSCAN min_cluster_size")
		root.AddCommand(c)
	}

	return root
}

// searchQueryOpts mirrors the query flags.
type searchQueryOpts struct {
	TopK        int
	JSON        bool
	WithCluster bool
}

func runSearchQuery(corpusID, q string, opts searchQueryOpts) error {
	root := searchProjectRoot()
	rt, err := config.Build(root, corpusID)
	if err != nil {
		return err
	}
	qc := qdrant.NewQdrantClient(rt.Cfg.Sidecars.QdrantURL)
	ec := embed.NewClient(rt.Cfg.Sidecars.LlamaRetrievalURL)
	env, err := query.Run(context.Background(), query.Config{
		Corpus:      rt.Corpus,
		Embedder:    &embed.QueryAdapter{Client: ec},
		Searcher:    &qdrant.Adapter{QdrantClient: qc},
		ProjectRoot: root,
		Query:       q,
		TopK:        opts.TopK,
		WithCluster: opts.WithCluster,
	})
	if err != nil {
		return err
	}
	if opts.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(env)
	}
	if env.Warning != "" {
		fmt.Fprintln(os.Stderr, "warning:", env.Warning)
	}
	for _, h := range env.Results {
		if h.LineStart > 0 {
			fmt.Printf("%.3f  %s:%d-%d  (%s)\n", h.Score, h.Path, h.LineStart, h.LineEnd, h.Kind)
		} else {
			fmt.Printf("%.3f  %s\n", h.Score, h.Path)
		}
	}
	return nil
}

func runSearchIndex(corpusID string, quiet bool) error {
	root := searchProjectRoot()
	rt, err := config.Build(root, corpusID)
	if err != nil {
		return err
	}
	qc := qdrant.NewQdrantClient(rt.Cfg.Sidecars.QdrantURL)
	ec := embed.NewClient(rt.Cfg.Sidecars.LlamaRetrievalURL)
	var progress func(done, total int)
	if !quiet {
		progress = func(done, total int) {
			fmt.Fprintf(os.Stderr, "\rindex: %d/%d", done, total)
		}
	}
	err = index.Run(context.Background(), index.Config{
		Corpus:      rt.Corpus,
		Embedder:    &embed.Adapter{Client: ec},
		Upserter:    &qdrant.Adapter{QdrantClient: qc},
		ProjectRoot: root,
		LockPath:    filepath.Join(root, rt.Cfg.Index.LockPath),
		Progress:    progress,
	})
	if !quiet {
		fmt.Fprintln(os.Stderr)
	}
	return err
}

func runSearchClusters(corpusID, coordsOut string, minPts int) error {
	root := searchProjectRoot()
	rt, err := config.Build(root, corpusID)
	if err != nil {
		return err
	}
	res, err := cluster.Run(context.Background(), cluster.Config{
		Corpus:       rt.Corpus,
		ProjectRoot:  root,
		QdrantURL:    rt.Cfg.Sidecars.QdrantURL,
		ReduceURL:    rt.Cfg.Sidecars.ReduceURL,
		HDBSCANURL:   rt.Cfg.Sidecars.HDBSCANURL,
		CoordsOutput: coordsOut,
		MinPts:       minPts,
	})
	if err != nil {
		return err
	}
	if res == nil {
		return nil
	}
	for _, c := range res.Clusters {
		fmt.Printf("cluster %d (size=%d): %s\n", c.ClusterID, c.Size, strings.Join(c.TopPaths, ", "))
	}
	return nil
}

func searchProjectRoot() string {
	cwd, _ := os.Getwd()
	out, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return cwd
	}
	return filepath.Clean(strings.TrimSpace(string(out)))
}
