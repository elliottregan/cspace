// Package main implements cspace-search, a standalone CLI for semantic search
// over commits and code. Produced alongside cspace-go so this binary can be
// dropped into any repo with a search.yaml.
package main

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
	"github.com/elliottregan/cspace/search/qdrant"
	"github.com/elliottregan/cspace/search/query"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{Use: "cspace-search", Short: "Semantic search over commits, code, and context"}
	root.AddCommand(indexCmd(), queryCmd(), clustersCmd())
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
		},
	}
	cmd.Flags().StringVar(&corpusID, "corpus", "code", "corpus id (code|commits|context)")
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
	cmd.Flags().StringVar(&corpusID, "corpus", "code", "corpus id (code|commits|context)")
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
	cmd.Flags().StringVar(&corpusID, "corpus", "code", "corpus id (code|commits|context)")
	cmd.Flags().StringVar(&coordsOut, "coords-out", "", "write TSV of coords+labels")
	cmd.Flags().IntVar(&minPts, "min-pts", 3, "HDBSCAN min_cluster_size (min points per cluster)")
	cmd.Flags().IntVar(&minSamples, "min-samples", 1, "HDBSCAN min_samples (cluster conservatism; higher → more noise, tighter clusters)")
	return cmd
}
