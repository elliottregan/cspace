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

// Load reads the embedded defaults and deep-merges an optional search.yaml
// from projectRoot on top. "Deep-merge" matters for nested maps like
// corpora.<id>: naively unmarshaling the override onto the existing Config
// replaces each touched CorpusConfig wholesale (so overriding just
// corpora.code.excludes would silently reset MaxBytes/Enabled to zero).
// Here we merge at the parsed-YAML tree level, then unmarshal the merged
// tree into the typed Config, so only keys actually present in the
// override take effect.
func Load(projectRoot string) (*Config, error) {
	var base map[string]any
	if err := yaml.Unmarshal(defaultYAML, &base); err != nil {
		return nil, err
	}

	path := filepath.Join(projectRoot, "search.yaml")
	if b, err := os.ReadFile(path); err == nil {
		var overlay map[string]any
		if err := yaml.Unmarshal(b, &overlay); err != nil {
			return nil, err
		}
		base = deepMerge(base, overlay)
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	merged, err := yaml.Marshal(base)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(merged, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// deepMerge recursively merges overlay into base: nested maps merge key by
// key (overlay wins on leaf conflicts), scalars and sequences from the
// overlay replace base values wholesale. Neither argument is mutated.
func deepMerge(base, overlay map[string]any) map[string]any {
	out := make(map[string]any, len(base))
	for k, v := range base {
		out[k] = v
	}
	for k, ov := range overlay {
		if bv, ok := out[k]; ok {
			bm, bIsMap := bv.(map[string]any)
			om, oIsMap := ov.(map[string]any)
			if bIsMap && oIsMap {
				out[k] = deepMerge(bm, om)
				continue
			}
		}
		out[k] = ov
	}
	return out
}
