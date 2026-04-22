package config

import (
	"errors"
	"fmt"

	"github.com/elliottregan/cspace/search/corpus"
)

// ErrCorpusDisabled signals that a corpus exists in config but is turned
// off for this project. Callers use errors.Is to distinguish an
// opted-out corpus from an infrastructure failure (sidecar unreachable,
// git ls-files failing, etc.) so init loops and CLI surfaces can report
// "disabled" vs "skipped" differently.
var ErrCorpusDisabled = errors.New("corpus disabled in search.yaml")

// ErrSearchDisabled signals that semantic search is not activated for
// this project (top-level `enabled:` is false in search.yaml, or there
// is no search.yaml). Supersedes ErrCorpusDisabled: if search is off at
// the project level, no individual corpus is checked. Keeps fresh
// cspace projects from incidentally indexing node_modules on first boot.
var ErrSearchDisabled = errors.New("search not configured for this project")

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
// Returns ErrSearchDisabled when the project hasn't opted in via top-level
// `enabled: true` in search.yaml; otherwise returns ErrCorpusDisabled
// (wrapped with the corpus id) when the named corpus has
// corpora.<id>.enabled=false. Single chokepoint so every caller (CLI
// queries, init loops, MCP tools, phase-16 bootstrap) refuses the
// operation with one consistent, actionable error.
func BuildWithConfig(projectRoot, corpusID string, cfg *Config) (*Runtime, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("%w (set `enabled: true` in %s/search.yaml to activate)", ErrSearchDisabled, projectRoot)
	}
	if cc, ok := cfg.Corpora[corpusID]; ok && !cc.Enabled {
		return nil, fmt.Errorf("%q: %w (set corpora.%s.enabled=true in search.yaml to enable)", corpusID, ErrCorpusDisabled, corpusID)
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
	case "context":
		return &corpus.ContextCorpus{}, nil
	case "issues":
		return &corpus.IssuesCorpus{Limit: cfg.Corpora["issues"].Limit}, nil
	default:
		return nil, fmt.Errorf("unknown corpus %q (known: code, commits, context, issues)", id)
	}
}
