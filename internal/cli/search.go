package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/search"
	"github.com/spf13/cobra"
)

func newSearchCmd() *cobra.Command {
	var llamaURL, qdrantURL string

	cmd := &cobra.Command{
		Use:     "search <query>",
		Short:   "Semantic search over git commit history",
		GroupID: "other",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			topN, _ := cmd.Flags().GetInt("top")
			return runSearch(strings.Join(args, " "), llamaURL, qdrantURL, topN)
		},
	}
	cmd.Flags().Int("top", 10, "Number of results to show")

	indexCmd := &cobra.Command{
		Use:   "index",
		Short: "Build or refresh the commit embedding index",
		RunE: func(cmd *cobra.Command, args []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			return runSearchIndex(llamaURL, qdrantURL, limit)
		},
	}
	indexCmd.Flags().Int("limit", 500, "Maximum number of commits to index")

	cmd.PersistentFlags().StringVar(&llamaURL, "llama-url", llamaURLDefault(), "llama.cpp server URL")
	cmd.PersistentFlags().StringVar(&qdrantURL, "qdrant-url", qdrantURLDefault(), "Qdrant server URL")
	cmd.AddCommand(indexCmd)
	return cmd
}

func llamaURLDefault() string {
	if u := os.Getenv("LLAMA_URL"); u != "" {
		return u
	}
	// Inside a cspace container the llama-server sidecar is reachable by service name.
	// Outside a container, override with --llama-url or LLAMA_URL.
	return "http://llama-server:8080"
}

func qdrantURLDefault() string {
	if u := os.Getenv("QDRANT_URL"); u != "" {
		return u
	}
	return "http://qdrant:6333"
}

func runSearchIndex(llamaURL, qdrantURL string, limit int) error {
	repoPath, err := os.Getwd()
	if err != nil {
		return err
	}

	fmt.Printf("Extracting commits from %s ...\n", repoPath)
	commits, err := search.ListCommits(repoPath, limit)
	if err != nil {
		return fmt.Errorf("reading git history: %w", err)
	}
	fmt.Printf("Found %d commits\n", len(commits))

	texts := make([]string, len(commits))
	for i, c := range commits {
		texts[i] = c.EmbedText()
	}

	fmt.Printf("Embedding %d commits via %s ...\n", len(texts), llamaURL)
	start := time.Now()
	embedClient := search.NewClient(llamaURL)
	vectors, err := embedClient.EmbedDocuments(texts, func(done, total int) {
		fmt.Printf("\r  %d / %d", done, total)
	})
	fmt.Println()
	if err != nil {
		return err
	}
	fmt.Printf("Embedded in %s\n", time.Since(start).Round(time.Millisecond))

	collection := search.CollectionName(repoPath)
	qdrant := search.NewQdrantClient(qdrantURL)

	fmt.Printf("Indexing into Qdrant collection %q ...\n", collection)
	if err := qdrant.DropCollection(collection); err != nil {
		return fmt.Errorf("dropping collection: %w", err)
	}
	dim := len(vectors[0])
	if err := qdrant.EnsureCollection(collection, dim); err != nil {
		return err
	}

	points := make([]search.QdrantPoint, len(commits))
	for i, c := range commits {
		points[i] = search.QdrantPoint{
			ID:     uint64(i),
			Vector: vectors[i],
			Payload: map[string]string{
				"hash":    c.Hash,
				"date":    c.Date.Format("2006-01-02"),
				"subject": c.Subject,
			},
		}
	}

	if err := qdrant.UpsertPoints(collection, points, 100, func(done, total int) {
		fmt.Printf("\r  upserted %d / %d", done, total)
	}); err != nil {
		return fmt.Errorf("upserting points: %w", err)
	}
	fmt.Printf("\nDone. %d commits indexed into %q\n", len(commits), collection)
	return nil
}

func runSearch(query, llamaURL, qdrantURL string, topN int) error {
	repoPath, err := os.Getwd()
	if err != nil {
		return err
	}

	embedClient := search.NewClient(llamaURL)
	queryVec, err := embedClient.EmbedQuery(query)
	if err != nil {
		return err
	}

	collection := search.CollectionName(repoPath)
	qdrant := search.NewQdrantClient(qdrantURL)
	results, err := qdrant.QueryPoints(collection, queryVec, topN)
	if err != nil {
		return fmt.Errorf("searching: %w\nRun `cspace search index` first if the collection doesn't exist", err)
	}

	fmt.Printf("Top %d commits in %s matching %q\n\n", len(results), filepath.Base(repoPath), query)
	for _, r := range results {
		fmt.Printf("%.2f  %s  %s  %s\n", r.Score, r.Hash[:7], r.Date, r.Subject)
	}
	return nil
}
