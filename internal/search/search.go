package search

// Result is a ranked commit returned by a search.
type Result struct {
	Score   float32
	Hash    string
	Date    string
	Subject string
}
