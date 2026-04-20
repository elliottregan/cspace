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
	default:
		return nil, fmt.Errorf("unknown corpus %q (known: code, commits)", id)
	}
}
