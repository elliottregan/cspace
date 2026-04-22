// Package main implements cspace-search, a standalone CLI for semantic search
// over commits and code. Produced alongside cspace-go so this binary can be
// dropped into any repo with a search.yaml.
package main

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
	"github.com/elliottregan/cspace/search/qdrant"
	"github.com/elliottregan/cspace/search/query"
	"github.com/elliottregan/cspace/search/status"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{Use: "cspace-search", Short: "Semantic search over commits, code, context, and issues"}
	root.AddCommand(indexCmd(), queryCmd(), clustersCmd(), initCmd(), statusCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func projectRoot() string {
	cwd, _ := os.Getwd()
	out, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return cwd
	}
	return filepath.Clean(strings.TrimSpace(string(out)))
}

func indexCmd() *cobra.Command {
	var corpusID string
	var quiet bool
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Build or refresh an index",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := projectRoot()
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
		},
	}
	cmd.Flags().StringVar(&corpusID, "corpus", "code", "corpus id (code|commits|context|issues)")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "suppress progress output")
	return cmd
}

func queryCmd() *cobra.Command {
	var corpusID string
	var topK int
	var asJSON bool
	var withCluster bool
	cmd := &cobra.Command{
		Use:   "query <query>",
		Short: "Run a semantic query",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := projectRoot()
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
				Query:       strings.Join(args, " "),
				TopK:        topK,
				WithCluster: withCluster,
			})
			if err != nil {
				return err
			}

			// Check staleness and annotate the envelope.
			appendStalenessWarning(env, corpusID, root, qc)

			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(env)
			}
			if env.Warning != "" {
				fmt.Fprintln(os.Stderr, "warning:", env.Warning)
			}
			for _, h := range env.Results {
				fmt.Printf("%.3f  %s:%d-%d  (%s)\n", h.Score, h.Path, h.LineStart, h.LineEnd, h.Kind)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&corpusID, "corpus", "code", "corpus id (code|commits|context|issues)")
	cmd.Flags().IntVar(&topK, "top", 10, "top K hits")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON envelope")
	cmd.Flags().BoolVar(&withCluster, "with-cluster", false, "include cluster_id per hit")
	return cmd
}

func clustersCmd() *cobra.Command {
	var corpusID string
	var coordsOut string
	var minPts int
	var minSamples int
	cmd := &cobra.Command{
		Use:   "clusters",
		Short: "Discover thematic clusters and write cluster_id to the index",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := projectRoot()
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
		},
	}
	cmd.Flags().StringVar(&corpusID, "corpus", "code", "corpus id (code|commits|context|issues)")
	cmd.Flags().StringVar(&coordsOut, "coords-out", "", "write TSV of coords+labels")
	cmd.Flags().IntVar(&minPts, "min-pts", 3, "HDBSCAN min_cluster_size (min points per cluster)")
	cmd.Flags().IntVar(&minSamples, "min-samples", 1, "HDBSCAN min_samples (cluster conservatism; higher → more noise, tighter clusters)")
	return cmd
}

// initCmd mirrors `cspace search init` for the standalone binary. Writes a
// project-local search.yaml template (if absent), installs lefthook hooks
// (if available), and runs an initial index over every enabled corpus,
// silently skipping ones whose sidecars / prerequisites aren't ready.
func initCmd() *cobra.Command {
	var quiet, skipYAML, skipHooks, skipIndex bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Bootstrap semantic search for the current project",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := projectRoot()
			report := func(format string, args ...any) {
				if !quiet {
					fmt.Fprintf(os.Stderr, "search init: "+format+"\n", args...)
				}
			}

			if !skipYAML {
				written, err := config.EnsureProjectYAML(root)
				switch {
				case err != nil:
					report("search.yaml: %v (continuing)", err)
				case written:
					report("wrote %s/search.yaml", root)
				default:
					report("search.yaml already present")
				}
			}

			if !skipHooks {
				installed, err := config.EnsureLefthookHooks(root)
				switch {
				case err != nil:
					report("lefthook: %v (continuing)", err)
				case installed:
					report("lefthook hooks installed")
				default:
					report("lefthook unavailable; auto-indexing will rely on manual `cspace-search index`")
				}
			}

			if !skipIndex {
				for _, corpusID := range []string{"code", "commits", "context", "issues"} {
					err := runIndexCorpus(cmd.Context(), root, corpusID)
					switch {
					case err == nil:
						report("%s: indexed", corpusID)
					case errors.Is(err, config.ErrCorpusDisabled):
						// runIndexCorpus already wrote the disabled state with
						// a fresh single-use writer — no outer writer needed.
						report("%s: disabled in search.yaml (enable with corpora.%s.enabled=true)", corpusID, corpusID)
					default:
						report("%s: skipped (%v)", corpusID, err)
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&quiet, "quiet", false, "suppress progress output")
	cmd.Flags().BoolVar(&skipYAML, "skip-yaml", false, "don't write search.yaml")
	cmd.Flags().BoolVar(&skipHooks, "skip-hooks", false, "don't install lefthook hooks")
	cmd.Flags().BoolVar(&skipIndex, "skip-index", false, "don't run initial index")
	return cmd
}

// runIndexCorpus is a thin wrapper that runs index.Run for one corpus id,
// used by initCmd to loop over corpora without rebuilding the cobra flag
// plumbing that indexCmd exposes.
func runIndexCorpus(ctx context.Context, root, corpusID string) error {
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
	sw, _ := status.NewWriter(root)
	var statusWriter index.StatusWriter
	if sw != nil {
		statusWriter = sw
	}
	return index.Run(ctx, index.Config{
		Corpus:       rt.Corpus,
		Embedder:     &embed.Adapter{Client: ec},
		Upserter:     &qdrant.Adapter{QdrantClient: qc},
		ProjectRoot:  root,
		LockPath:     filepath.Join(root, rt.Cfg.Index.LockPath),
		StatusWriter: statusWriter,
	})
}

func statusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show index status and staleness for all corpora",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := projectRoot()
			cfg, err := config.Load(root)
			if err != nil {
				return err
			}
			sf, err := status.Read(root)
			if err != nil {
				return fmt.Errorf("reading status file: %w", err)
			}

			type corpusStatusOutput struct {
				State        string `json:"state"`
				FinishedAt   string `json:"finished_at,omitempty"`
				DurationMS   int64  `json:"duration_ms,omitempty"`
				IndexedCount int    `json:"indexed_count,omitempty"`
				Error        string `json:"error,omitempty"`
				Stale        bool   `json:"stale,omitempty"`
				StaleReason  string `json:"stale_reason,omitempty"`
			}
			type statusOutput struct {
				Corpora map[string]corpusStatusOutput `json:"corpora"`
				Current *status.RunningState          `json:"current"`
			}

			allCorpora := []string{"code", "commits", "context", "issues"}
			out := statusOutput{Corpora: make(map[string]corpusStatusOutput)}
			if sf != nil {
				out.Current = sf.Current
			}

			for _, id := range allCorpora {
				co := corpusStatusOutput{State: "unknown"}
				if cc, ok := cfg.Corpora[id]; ok && !cc.Enabled {
					co.State = "disabled"
					out.Corpora[id] = co
					continue
				}
				if sf != nil {
					if cs, ok := sf.Last[id]; ok {
						co.State = cs.State
						if !cs.FinishedAt.IsZero() {
							co.FinishedAt = cs.FinishedAt.Format(time.RFC3339)
						}
						co.DurationMS = cs.DurationMS
						co.IndexedCount = cs.IndexedCount
						co.Error = cs.Error
					}
				}
				if co.State == "completed" && (id == "code" || id == "commits") {
					qc := qdrant.NewQdrantClient(cfg.Sidecars.QdrantURL)
					adapter := &qdrant.Adapter{QdrantClient: qc}
					collection := corpusCollection(id, root)
					var st corpus.Staleness
					switch id {
					case "code":
						st, _ = corpus.CodeStaleness(root, collection, adapter)
					case "commits":
						st, _ = corpus.CommitsStaleness(root, collection, adapter)
					}
					if st.IsStale {
						co.Stale = true
						co.StaleReason = st.Reason
					}
				}
				out.Corpora[id] = co
			}

			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}

			for _, id := range allCorpora {
				co := out.Corpora[id]
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

			if out.Current != nil {
				age := timeAgo(time.Since(out.Current.StartedAt))
				fmt.Printf("\nCurrently running: %s  started %s ago", out.Current.Corpus, age)
				if out.Current.Progress.Total > 0 {
					fmt.Printf("  (%d/%d)", out.Current.Progress.Done, out.Current.Progress.Total)
				}
				fmt.Println()
			} else {
				fmt.Println("\nCurrently running: none.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON output for programmatic consumers")
	return cmd
}

// appendStalenessWarning checks corpus staleness and appends a warning to
// the envelope if the index is out of date.
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
		st, err = corpus.CodeStaleness(root, collection, adapter)
	case "commits":
		st, err = corpus.CommitsStaleness(root, collection, adapter)
	default:
		return
	}
	if err != nil || !st.IsStale {
		return
	}
	warning := "index may be out of date: " + st.Reason +
		" \u2014 run `cspace search " + corpusID + " index` to refresh"
	env.Warning = strings.TrimSpace(env.Warning + "\n" + warning)
}

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
