package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elliottregan/cspace/search/cluster"
	corpuspkg "github.com/elliottregan/cspace/search/corpus"
	"github.com/elliottregan/cspace/search/embed"
	qdrantpkg "github.com/elliottregan/cspace/search/qdrant"
	"github.com/elliottregan/cspace/search/reduce"
	"github.com/spf13/cobra"
)

func newSearchCmd() *cobra.Command {
	var llamaURL, clusterURL, qdrantURL, reduceURL, hdbscanURL string

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

	clustersCmd := &cobra.Command{
		Use:   "clusters",
		Short: "Discover thematic clusters in commit history (UMAP → HDBSCAN)",
		RunE: func(cmd *cobra.Command, args []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			minPts, _ := cmd.Flags().GetInt("min-pts")
			topPer, _ := cmd.Flags().GetInt("top-per-cluster")
			coordsOut, _ := cmd.Flags().GetString("coords-out")
			return runSearchClusters(clusterURL, qdrantURL, reduceURL, hdbscanURL, limit, minPts, topPer, coordsOut)
		},
	}
	clustersCmd.Flags().Int("limit", 500, "Maximum number of commits to index")
	clustersCmd.Flags().Int("min-pts", 3, "Minimum commits to form a cluster (HDBSCAN min_cluster_size)")
	clustersCmd.Flags().Int("top-per-cluster", 6, "Commits to show per cluster")
	clustersCmd.Flags().String("coords-out", "", "Write per-commit 2D coordinates to this TSV file (hash\tx\ty\tlabel\tsubject)")

	cmd.PersistentFlags().StringVar(&llamaURL, "llama-url", llamaURLDefault(), "llama.cpp server URL (retrieval adapter)")
	cmd.PersistentFlags().StringVar(&clusterURL, "cluster-url", clusterURLDefault(), "llama.cpp server URL (clustering adapter)")
	cmd.PersistentFlags().StringVar(&qdrantURL, "qdrant-url", qdrantURLDefault(), "Qdrant server URL")
	cmd.PersistentFlags().StringVar(&reduceURL, "reduce-url", reduceURLDefault(), "Dimension reduction service URL (reduce-api)")
	cmd.PersistentFlags().StringVar(&hdbscanURL, "hdbscan-url", hdbscanURLDefault(), "HDBSCAN clustering service URL")
	cmd.AddCommand(indexCmd)
	cmd.AddCommand(clustersCmd)
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

func clusterURLDefault() string {
	if u := os.Getenv("CLUSTER_URL"); u != "" {
		return u
	}
	return "http://llama-clustering:8080"
}

func reduceURLDefault() string {
	if u := os.Getenv("REDUCE_URL"); u != "" {
		return u
	}
	return "http://reduce-api:8000"
}

func hdbscanURLDefault() string {
	if u := os.Getenv("HDBSCAN_URL"); u != "" {
		return u
	}
	return "http://hdbscan-api:8090"
}

func runSearchIndex(llamaURL, qdrantURL string, limit int) error {
	repoPath, err := os.Getwd()
	if err != nil {
		return err
	}

	fmt.Printf("Extracting commits from %s ...\n", repoPath)
	cc := &corpuspkg.CommitCorpus{Limit: limit}
	recCh, errCh := cc.Enumerate(repoPath)

	var records []corpuspkg.Record
	for rec := range recCh {
		records = append(records, rec)
	}
	for e := range errCh {
		return fmt.Errorf("reading git history: %w", e)
	}
	fmt.Printf("Found %d commits\n", len(records))

	texts := make([]string, len(records))
	for i, r := range records {
		texts[i] = r.EmbedText
	}

	fmt.Printf("Embedding %d commits via %s ...\n", len(texts), llamaURL)
	start := time.Now()
	embedClient := embed.NewClient(llamaURL)
	vectors, err := embedClient.EmbedDocuments(texts, func(done, total int) {
		fmt.Printf("\r  %d / %d", done, total)
	})
	fmt.Println()
	if err != nil {
		return err
	}
	fmt.Printf("Embedded in %s\n", time.Since(start).Round(time.Millisecond))

	collection := "git-search-" + corpuspkg.ProjectHash(repoPath)
	qdrant := qdrantpkg.NewQdrantClient(qdrantURL)

	fmt.Printf("Indexing into Qdrant collection %q ...\n", collection)
	if err := qdrant.DropCollection(collection); err != nil {
		return fmt.Errorf("dropping collection: %w", err)
	}
	dim := len(vectors[0])
	if err := qdrant.EnsureCollection(collection, dim); err != nil {
		return err
	}

	points := make([]qdrantpkg.QdrantPoint, len(records))
	for i, r := range records {
		points[i] = qdrantpkg.QdrantPoint{
			ID:     uint64(i),
			Vector: vectors[i],
			Payload: r.Extra,
		}
	}

	if err := qdrant.UpsertPoints(collection, points, 100, func(done, total int) {
		fmt.Printf("\r  upserted %d / %d", done, total)
	}); err != nil {
		return fmt.Errorf("upserting points: %w", err)
	}
	fmt.Printf("\nDone. %d commits indexed into %q\n", len(records), collection)
	return nil
}

func runSearch(query, llamaURL, qdrantURL string, topN int) error {
	repoPath, err := os.Getwd()
	if err != nil {
		return err
	}

	embedClient := embed.NewClient(llamaURL)
	queryVec, err := embedClient.EmbedQuery(query)
	if err != nil {
		return err
	}

	collection := "git-search-" + corpuspkg.ProjectHash(repoPath)
	qdrant := qdrantpkg.NewQdrantClient(qdrantURL)
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

// clusterCollectionName returns the Qdrant collection name for clustering
// embeddings (separate from retrieval to keep the two vector spaces apart).
func clusterCollectionName(repoPath string) string {
	return "git-search-" + corpuspkg.ProjectHash(repoPath) + "-clustering"
}

func runSearchClusters(clusterURL, qdrantURL, reduceURL, hdbscanURL string, limit, minPts, topPer int, coordsOut string) error {
	repoPath, err := os.Getwd()
	if err != nil {
		return err
	}

	collection := clusterCollectionName(repoPath)
	qdrant := qdrantpkg.NewQdrantClient(qdrantURL)

	// Index-if-empty: if the clustering collection doesn't exist, build it.
	points, err := qdrant.ScrollPoints(collection)
	if err != nil || len(points) == 0 {
		if err := buildClusteringIndex(clusterURL, qdrant, collection, repoPath, limit); err != nil {
			return err
		}
		points, err = qdrant.ScrollPoints(collection)
		if err != nil {
			return err
		}
	}
	fmt.Printf("Loaded %d commits from %q\n", len(points), collection)

	vectors := make([][]float32, len(points))
	for i, p := range points {
		vectors[i] = p.Vector
	}

	fmt.Printf("Reducing %d vectors to 2D via %s ...\n", len(vectors), reduceURL)
	start := time.Now()
	reducer := reduce.NewReduceClient(reduceURL)
	coords, err := reducer.Reduce(vectors, 2)
	if err != nil {
		return err
	}
	fmt.Printf("Reduced in %s\n", time.Since(start).Round(time.Millisecond))

	fmt.Printf("Clustering 2D points via %s ...\n", hdbscanURL)
	start = time.Now()
	hd := cluster.NewHdbscanClient(hdbscanURL)
	labels, err := hd.Cluster(coords, minPts, 1)
	if err != nil {
		return err
	}
	fmt.Printf("Clustered in %s\n\n", time.Since(start).Round(time.Millisecond))

	clusters, noise := cluster.BuildClusters(points, coords, labels)

	if coordsOut != "" {
		if err := writeCoordsTSV(coordsOut, points, coords, labels); err != nil {
			return fmt.Errorf("writing coords: %w", err)
		}
		fmt.Printf("2D coordinates written to %s\n\n", coordsOut)
	}

	fmt.Printf("Found %d clusters (%d noise points) ranked by size × density\n\n", len(clusters), noise)
	for _, c := range clusters {
		fmt.Printf("=== Cluster %d  (size: %d, density: %.2f) ===\n", c.ID, len(c.Members), c.Density)
		n := topPer
		if n > len(c.Members) {
			n = len(c.Members)
		}
		for _, m := range c.Members[:n] {
			fmt.Printf("  %s  %s  %s\n", m.Hash[:7], m.Date, m.Subject)
		}
		if len(c.Members) > n {
			fmt.Printf("  ... (%d more)\n", len(c.Members)-n)
		}
		fmt.Println()
	}
	return nil
}

func buildClusteringIndex(clusterURL string, qdrant *qdrantpkg.QdrantClient, collection, repoPath string, limit int) error {
	fmt.Printf("Clustering collection %q not found — building it now\n", collection)
	fmt.Printf("Extracting commits from %s ...\n", repoPath)
	cc := &corpuspkg.CommitCorpus{Limit: limit}
	recCh, errCh := cc.Enumerate(repoPath)

	var records []corpuspkg.Record
	for rec := range recCh {
		records = append(records, rec)
	}
	for e := range errCh {
		return fmt.Errorf("reading git history: %w", e)
	}
	fmt.Printf("Found %d commits\n", len(records))

	texts := make([]string, len(records))
	for i, r := range records {
		texts[i] = r.EmbedText
	}

	fmt.Printf("Embedding (clustering adapter) via %s ...\n", clusterURL)
	start := time.Now()
	embedClient := embed.NewClient(clusterURL)
	vectors, err := embedClient.EmbedPlain(texts, func(done, total int) {
		fmt.Printf("\r  %d / %d", done, total)
	})
	fmt.Println()
	if err != nil {
		return err
	}
	fmt.Printf("Embedded in %s\n", time.Since(start).Round(time.Millisecond))

	if err := qdrant.DropCollection(collection); err != nil {
		return err
	}
	if err := qdrant.EnsureCollection(collection, len(vectors[0])); err != nil {
		return err
	}

	qps := make([]qdrantpkg.QdrantPoint, len(records))
	for i, r := range records {
		qps[i] = qdrantpkg.QdrantPoint{
			ID:     uint64(i),
			Vector: vectors[i],
			Payload: r.Extra,
		}
	}
	if err := qdrant.UpsertPoints(collection, qps, 100, func(done, total int) {
		fmt.Printf("\r  upserted %d / %d", done, total)
	}); err != nil {
		return err
	}
	fmt.Printf("\n")
	return nil
}

func writeCoordsTSV(path string, points []qdrantpkg.ScrolledPoint, coords [][]float32, labels []int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	fmt.Fprintln(f, "hash\tx\ty\tlabel\tdate\tsubject")
	for i, p := range points {
		fmt.Fprintf(f, "%s\t%f\t%f\t%d\t%s\t%s\n",
			p.Hash, coords[i][0], coords[i][1], labels[i], p.Date, p.Subject)
	}
	return nil
}
