// Package config loads search.yaml with defaults merged in.
package config

import (
	_ "embed"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

//go:embed default.yaml
var defaultYAML []byte

// Config is the top-level config shape.
type Config struct {
	Corpora  map[string]CorpusConfig `yaml:"corpora"`
	Sidecars Sidecars                `yaml:"sidecars"`
	Index    IndexConfig             `yaml:"index"`
}

// CorpusConfig is per-corpus configuration.
type CorpusConfig struct {
	Enabled  bool     `yaml:"enabled"`
	MaxBytes int64    `yaml:"max_bytes"`
	Excludes []string `yaml:"excludes"`
	Limit    int      `yaml:"limit"`
}

// Sidecars holds external service URLs.
type Sidecars struct {
	LlamaRetrievalURL  string `yaml:"llama_retrieval_url"`
	LlamaClusteringURL string `yaml:"llama_clustering_url"`
	QdrantURL          string `yaml:"qdrant_url"`
	ReduceURL          string `yaml:"reduce_url"`
	HDBSCANURL         string `yaml:"hdbscan_url"`
}

// IndexConfig holds indexer runtime paths.
type IndexConfig struct {
	LockPath string `yaml:"lock_path"`
	LogPath  string `yaml:"log_path"`
}

// Load reads the embedded defaults and then shallow-merges an optional
// search.yaml from projectRoot on top. Missing fields in the override
// preserve default values.
func Load(projectRoot string) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(defaultYAML, &cfg); err != nil {
		return nil, err
	}
	path := filepath.Join(projectRoot, "search.yaml")
	b, err := os.ReadFile(path)
	if err == nil {
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return &cfg, nil
}
