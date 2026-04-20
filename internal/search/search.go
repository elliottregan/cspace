package search

import (
	"math"
	"sort"
)

// Result is a ranked commit from a search.
type Result struct {
	Score   float32
	Hash    string
	Date    string
	Subject string
}

// Search returns the top topN entries by cosine similarity to queryVec.
func Search(idx *Index, queryVec []float32, topN int) []Result {
	type scored struct {
		score float32
		entry IndexEntry
	}

	results := make([]scored, 0, len(idx.Entries))
	for _, e := range idx.Entries {
		s := CosineSimilarity(queryVec, e.Vector)
		results = append(results, scored{s, e})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if topN > len(results) {
		topN = len(results)
	}

	out := make([]Result, topN)
	for i, r := range results[:topN] {
		out[i] = Result{
			Score:   r.score,
			Hash:    r.entry.Hash,
			Date:    r.entry.Date.Format("2006-01-02"),
			Subject: r.entry.Subject,
		}
	}
	return out
}

// CosineSimilarity returns the cosine similarity between two vectors.
// Returns 0 if either vector has zero magnitude.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, magA, magB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		magA += float64(a[i]) * float64(a[i])
		magB += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(magA) * math.Sqrt(magB)
	if denom == 0 {
		return 0
	}
	return float32(dot / denom)
}
