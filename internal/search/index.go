package search

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// IndexEntry is one commit stored in the index.
type IndexEntry struct {
	Hash    string    `json:"hash"`
	Date    time.Time `json:"date"`
	Subject string    `json:"subject"`
	Vector  []float32 `json:"vector"`
}

// Index is the full on-disk embedding index for a repository.
type Index struct {
	ModelURL  string       `json:"model_url"`
	CreatedAt time.Time    `json:"created_at"`
	Entries   []IndexEntry `json:"entries"`
}

// IndexPath returns the path where the index for repoPath is stored.
func IndexPath(cspaceHome, repoPath string) string {
	abs, _ := filepath.Abs(repoPath)
	h := sha256.Sum256([]byte(abs))
	return filepath.Join(cspaceHome, "search", fmt.Sprintf("%x", h[:4]), "index.json")
}

// Load reads an index from disk. Returns os.ErrNotExist if not yet built.
func Load(path string) (*Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parsing index: %w", err)
	}
	return &idx, nil
}

// Save writes the index atomically to path (creates parent dirs as needed).
func Save(path string, idx *Index) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
