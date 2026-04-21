package config

import (
	"fmt"

	"github.com/elliottregan/cspace/search/corpus"
)

// Runtime bundles a loaded config and a chosen Corpus for one command run.
type Runtime struct {
	Cfg    *Config
	Corpus corpus.Corpus
}

// Build loads config from projectRoot and instantiates the requested corpus.
func Build(projectRoot, corpusID string) (*Runtime, error) {
	cfg, err := Load(projectRoot)
	if err != nil {
		return nil, err
	}
	return BuildWithConfig(projectRoot, corpusID, cfg)
}

// BuildWithConfig instantiates a Runtime using an already-loaded *Config,
// avoiding a redundant disk read when the caller already holds the config.
func BuildWithConfig(projectRoot, corpusID string, cfg *Config) (*Runtime, error) {
	c, err := buildCorpus(corpusID, cfg)
	if err != nil {
		return nil, err
	}
	return &Runtime{Cfg: cfg, Corpus: c}, nil
}

func buildCorpus(id string, cfg *Config) (corpus.Corpus, error) {
	switch id {
	case "code":
		return &corpus.CodeCorpus{
			Filter: corpus.Filter{
				MaxBytes: cfg.Corpora["code"].MaxBytes,
				Excludes: cfg.Corpora["code"].Excludes,
			},
			Chunk: corpus.ChunkConfig{Max: 12000, Overlap: 200},
		}, nil
	case "commits":
		return &corpus.CommitCorpus{Limit: cfg.Corpora["commits"].Limit}, nil
	case "context":
		return &corpus.ContextCorpus{}, nil
	default:
		return nil, fmt.Errorf("unknown corpus %q (known: code, commits, context)", id)
	}
}
