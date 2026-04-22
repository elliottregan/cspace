package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/elliottregan/cspace/search/cluster"
	"github.com/elliottregan/cspace/search/config"
	"github.com/elliottregan/cspace/search/corpus"
	"github.com/elliottregan/cspace/search/embed"
	"github.com/elliottregan/cspace/search/index"
	searchmcp "github.com/elliottregan/cspace/search/mcp"
	"github.com/elliottregan/cspace/search/qdrant"
	"github.com/elliottregan/cspace/search/query"
	"github.com/elliottregan/cspace/search/status"

	mcpSDK "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func newSearchCmd() *cobra.Command {
	var topK int
	cmd := &cobra.Command{
		Use:     "search",
		Short:   "Semantic search over commits and code",
		Long:    "Subcommands: code, commits, context. Back-compat: `cspace search \"<query>\"` runs a commits query.",
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
	cmd.AddCommand(
		newSearchSubcmd("code"),
		newSearchSubcmd("commits"),
		newSearchSubcmd("context"),
		newSearchSubcmd("issues"),
		newSearchMCPCmd(),
		newSearchInitCmd(),
		newSearchStatusCmd(),
	)
	return cmd
}

// newSearchMCPCmd builds `cspace search mcp`, a stdio MCP server exposing
// the search tools (search_code, search_context, search_issues,
// list_clusters). Registered per agent container via init-claude-plugins.sh
// so advisors/coordinators/implementers can consult the indexes mid-session.
// Parallels `cspace context-server`.
func newSearchMCPCmd() *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the search MCP server over stdio",
		Long: `Expose the code, commits, context, and issues search indexes as MCP
tools (search_code, search_context, search_issues, list_clusters). Invoked
by Claude Code via .mcp.json or a container's Claude MCP config, not by
humans directly.`,
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
		var minSamples int
		c := &cobra.Command{
			Use:   "clusters",
			Short: "Discover clusters and write cluster_id to index",
			RunE: func(cmd *cobra.Command, args []string) error {
				return runSearchClusters(corpusID, coordsOut, minPts, minSamples)
			},
		}
		c.Flags().StringVar(&coordsOut, "coords-out", "", "write TSV of coords+labels")
		c.Flags().IntVar(&minPts, "min-pts", 3, "HDBSCAN min_cluster_size (min points per cluster)")
		c.Flags().IntVar(&minSamples, "min-samples", 1, "HDBSCAN min_samples (cluster conservatism; higher → more noise, tighter clusters)")
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

	// Check staleness and annotate the envelope.
	appendStalenessWarning(env, corpusID, root, qc)

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

// appendStalenessWarning checks corpus staleness and appends a warning to
// the envelope if the index is out of date. Best-effort: errors are silently
// ignored so queries are never blocked by staleness checks.
func appendStalenessWarning(env *query.Envelope, corpusID, root string, qc *qdrant.QdrantClient) {
	adapter := &qdrant.Adapter{QdrantClient: qc}
	collection := corpusCollection(corpusID, root)
	if collection == "" {
		return
	}
	var st corpus.Staleness
	var err error
	switch corpusID {
	case "code":
		st, err = corpus.CodeStalenessCached(root, collection, adapter)
	case "commits":
		st, err = corpus.CommitsStalenessCached(root, collection, adapter)
	default:
		return // staleness not implemented for context/issues
	}
	if err != nil || !st.IsStale {
		return
	}
	warning := "index may be out of date: " + st.Reason +
		" \u2014 run `cspace search " + corpusID + " index` to refresh"
	env.Warning = strings.TrimSpace(env.Warning + "\n" + warning)
}

// corpusCollection returns the qdrant collection name for a corpus, or ""
// if the corpus ID is unknown. Used to avoid importing config.Build again
// just for the collection name.
func corpusCollection(corpusID, projectRoot string) string {
	switch corpusID {
	case "code":
		return "code-" + corpus.ProjectHash(projectRoot)
	case "commits":
		return "commits-" + corpus.ProjectHash(projectRoot)
	default:
		return ""
	}
}

func runSearchIndex(corpusID string, quiet bool) error {
	root := searchProjectRoot()
	rt, err := config.Build(root, corpusID)
	if err != nil {
		// If the corpus is disabled, record that in the status file with a
		// fresh single-use writer so we never clobber state written by prior
		// iterations. Then return the sentinel so callers can report it.
		if errors.Is(err, config.ErrCorpusDisabled) {
			if sw, swErr := status.NewWriter(root); swErr == nil && sw != nil {
				sw.DisableCorpus(corpusID)
			}
		}
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
	sw, _ := status.NewWriter(root)
	var statusWriter index.StatusWriter
	if sw != nil {
		statusWriter = sw
	}
	err = index.Run(context.Background(), index.Config{
		Corpus:       rt.Corpus,
		Embedder:     &embed.Adapter{Client: ec},
		Upserter:     &qdrant.Adapter{QdrantClient: qc},
		ProjectRoot:  root,
		LockPath:     filepath.Join(root, rt.Cfg.Index.LockPath),
		Progress:     progress,
		StatusWriter: statusWriter,
	})
	if !quiet {
		fmt.Fprintln(os.Stderr)
	}
	return err
}

func runSearchClusters(corpusID, coordsOut string, minPts, minSamples int) error {
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
		MinSamples:   minSamples,
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

// newSearchStatusCmd builds `cspace search status`.
func newSearchStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show index status and staleness for all corpora",
		Long: `Reads .cspace/search-index-status.json and checks each corpus for
staleness against the current git state. Shows whether each corpus is
completed, failed, disabled, or currently running, plus whether the
index is out of date.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSearchStatus(asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON output for programmatic consumers")
	return cmd
}

func runSearchStatus(asJSON bool) error {
	root := searchProjectRoot()
	cfg, err := config.Load(root)
	if err != nil {
		return err
	}

	disabled := make(map[string]bool)
	for id, cc := range cfg.Corpora {
		if !cc.Enabled {
			disabled[id] = true
		}
	}

	cs, err := status.Compute(root, cfg.Enabled, disabled, func(corpusID string) (bool, string) {
		qc := qdrant.NewQdrantClient(cfg.Sidecars.QdrantURL)
		adapter := &qdrant.Adapter{QdrantClient: qc}
		collection := corpusCollection(corpusID, root)
		var st corpus.Staleness
		switch corpusID {
		case "code":
			st, _ = corpus.CodeStalenessCached(root, collection, adapter)
		case "commits":
			st, _ = corpus.CommitsStalenessCached(root, collection, adapter)
		}
		return st.IsStale, st.Reason
	})
	if err != nil {
		return fmt.Errorf("computing status: %w", err)
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(cs)
	}

	// When search is opted out project-wide, show a single clear message
	// instead of iterating the corpus map (which is empty by contract).
	if !cs.Enabled {
		fmt.Printf("search not configured for this project.\n")
		fmt.Printf("To activate, set `enabled: true` in %s/search.yaml.\n", root)
		return nil
	}

	// Human-readable output.
	allCorpora := []string{"code", "commits", "context", "issues"}
	for _, id := range allCorpora {
		co := cs.Corpora[id]
		switch co.State {
		case "disabled":
			fmt.Printf("%-10s disabled (enable with corpora.%s.enabled=true in search.yaml)\n", id, id)
		case "completed":
			age := "unknown"
			if co.FinishedAt != "" {
				if t, err := time.Parse(time.RFC3339, co.FinishedAt); err == nil {
					age = timeAgo(time.Since(t))
				}
			}
			line := fmt.Sprintf("%-10s completed %s ago", id, age)
			if co.IndexedCount > 0 {
				line += fmt.Sprintf("   (%d chunks indexed)", co.IndexedCount)
			}
			if co.Stale {
				line += fmt.Sprintf(" — STALE: %s", co.StaleReason)
			} else {
				line += " — up to date"
			}
			fmt.Println(line)
		case "failed":
			age := "unknown"
			if co.FinishedAt != "" {
				if t, err := time.Parse(time.RFC3339, co.FinishedAt); err == nil {
					age = timeAgo(time.Since(t))
				}
			}
			fmt.Printf("%-10s failed %s ago   error: %s\n", id, age, co.Error)
		default:
			fmt.Printf("%-10s never indexed\n", id)
		}
	}

	// Show current run.
	if cs.Current != nil {
		age := timeAgo(time.Since(cs.Current.StartedAt))
		fmt.Printf("\nCurrently running: %s  started %s ago", cs.Current.Corpus, age)
		if cs.Current.Progress.Total > 0 {
			fmt.Printf("  (%d/%d)", cs.Current.Progress.Done, cs.Current.Progress.Total)
		}
		fmt.Println()
	} else {
		fmt.Println("\nCurrently running: none.")
	}

	return nil
}

// timeAgo returns a human-friendly duration string.
func timeAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
