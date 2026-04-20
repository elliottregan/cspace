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
	var llamaURL string

	cmd := &cobra.Command{
		Use:     "search <query>",
		Short:   "Semantic search over git commit history",
		GroupID: "other",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.Join(args, " ")
			topN, _ := cmd.Flags().GetInt("top")
			return runSearch(query, llamaURL, topN)
		},
	}

	indexCmd := &cobra.Command{
		Use:   "index",
		Short: "Build or refresh the commit embedding index",
		RunE: func(cmd *cobra.Command, args []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			return runSearchIndex(llamaURL, limit)
		},
	}
	indexCmd.Flags().Int("limit", 500, "Maximum number of commits to index")

	cmd.Flags().Int("top", 10, "Number of results to show")
	cmd.PersistentFlags().StringVar(&llamaURL, "llama-url", llamaURLDefault(), "llama.cpp server URL")

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

func runSearchIndex(llamaURL string, limit int) error {
	repoPath, err := os.Getwd()
	if err != nil {
		return err
	}

	cspaceHome, err := defaultCspaceHome()
	if err != nil {
		return err
	}

	fmt.Printf("Extracting commits from %s ...\n", repoPath)
	commits, err := search.ListCommits(repoPath, limit)
	if err != nil {
		return fmt.Errorf("reading git history: %w", err)
	}
	fmt.Printf("Found %d commits\n", len(commits))

	client := search.NewClient(llamaURL)

	texts := make([]string, len(commits))
	for i, c := range commits {
		texts[i] = c.EmbedText()
	}

	fmt.Printf("Embedding %d commits via %s (batched) ...\n", len(texts), llamaURL)
	start := time.Now()
	vectors, err := client.EmbedWithProgress(texts, func(done, total int) {
		fmt.Printf("\r  %d / %d", done, total)
	})
	fmt.Println()
	if err != nil {
		return err
	}
	fmt.Printf("Done in %s\n", time.Since(start).Round(time.Millisecond))

	entries := make([]search.IndexEntry, len(commits))
	for i, c := range commits {
		entries[i] = search.IndexEntry{
			Hash:    c.Hash,
			Date:    c.Date,
			Subject: c.Subject,
			Vector:  vectors[i],
		}
	}

	idx := &search.Index{
		ModelURL:  llamaURL,
		CreatedAt: time.Now(),
		Entries:   entries,
	}

	idxPath := search.IndexPath(cspaceHome, repoPath)
	if err := search.Save(idxPath, idx); err != nil {
		return fmt.Errorf("saving index: %w", err)
	}

	fmt.Printf("Index saved to %s\n", idxPath)
	return nil
}

func runSearch(query, llamaURL string, topN int) error {
	repoPath, err := os.Getwd()
	if err != nil {
		return err
	}

	cspaceHome, err := defaultCspaceHome()
	if err != nil {
		return err
	}

	idxPath := search.IndexPath(cspaceHome, repoPath)
	idx, err := search.Load(idxPath)
	if os.IsNotExist(err) {
		return fmt.Errorf("no index found — run `cspace search index` first")
	}
	if err != nil {
		return fmt.Errorf("loading index: %w", err)
	}

	client := search.NewClient(llamaURL)
	vecs, err := client.Embed([]string{query})
	if err != nil {
		return err
	}

	results := search.Search(idx, vecs[0], topN)

	shortRepo := filepath.Base(repoPath)
	fmt.Printf("Top %d commits in %s matching %q\n\n", len(results), shortRepo, query)
	for _, r := range results {
		fmt.Printf("%.2f  %s  %s  %s\n", r.Score, r.Hash[:7], r.Date, r.Subject)
	}
	return nil
}
