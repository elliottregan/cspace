package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/elliottregan/cspace/search/config"
	"github.com/spf13/cobra"
)

// searchInitOpts bundles flags for the init command.
type searchInitOpts struct {
	Quiet     bool
	SkipYAML  bool
	SkipHooks bool
	SkipIndex bool
}

// newSearchInitCmd builds `cspace search init`.
func newSearchInitCmd() *cobra.Command {
	var opts searchInitOpts
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Bootstrap semantic search for the current project",
		Long: `Bootstraps search in three idempotent steps:

  1. Writes a minimal search.yaml at the project root (skipped if one
     already exists). Defaults still ship embedded in the binary; the
     project file only needs keys the project wants to override.
  2. Installs lefthook hooks for auto-indexing on commit/checkout/merge
     (skipped if lefthook is not on PATH, or the project has no
     lefthook.yml).
  3. Runs an initial index over every enabled corpus, silently skipping
     ones whose sidecars are unreachable or whose prerequisites are
     missing (e.g. GH_TOKEN for the issues corpus).

Safe to re-run. Used by cspace up's provisioning tail to self-initialize
new instances.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSearchInit(opts)
		},
	}
	cmd.Flags().BoolVar(&opts.Quiet, "quiet", false, "suppress progress output")
	cmd.Flags().BoolVar(&opts.SkipYAML, "skip-yaml", false, "don't write search.yaml")
	cmd.Flags().BoolVar(&opts.SkipHooks, "skip-hooks", false, "don't install lefthook hooks")
	cmd.Flags().BoolVar(&opts.SkipIndex, "skip-index", false, "don't run initial index")
	return cmd
}

func runSearchInit(opts searchInitOpts) error {
	root := searchProjectRoot()
	report := func(format string, args ...any) {
		if !opts.Quiet {
			fmt.Fprintf(os.Stderr, "search init: "+format+"\n", args...)
		}
	}

	if !opts.SkipYAML {
		written, err := config.EnsureProjectYAML(root)
		switch {
		case err != nil:
			report("search.yaml: %v (continuing)", err)
		case written:
			report("wrote %s", filepath.Join(root, "search.yaml"))
		default:
			report("search.yaml already present")
		}
	}

	if !opts.SkipHooks {
		installed, err := config.EnsureLefthookHooks(root)
		switch {
		case err != nil:
			report("lefthook: %v (continuing)", err)
		case installed:
			report("lefthook hooks installed")
		default:
			report("lefthook unavailable; auto-indexing will rely on manual `cspace search <corpus> index`")
		}
	}

	if !opts.SkipIndex {
		for _, corpusID := range []string{"code", "commits", "context", "issues"} {
			err := runSearchIndex(corpusID, true)
			switch {
			case err == nil:
				report("%s: indexed", corpusID)
			case errors.Is(err, config.ErrCorpusDisabled):
				report("%s: disabled in search.yaml (enable with corpora.%s.enabled=true)", corpusID, corpusID)
			default:
				report("%s: skipped (%v)", corpusID, err)
			}
		}
	}
	return nil
}
