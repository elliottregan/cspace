package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestSearchCommand_HelpShowsSubcommands verifies the search subcommand tree.
// The root `cspace search --help` shows "code" and "commits" corpus subcommands.
// Each corpus subcommand exposes "query", "index", and "clusters".
func TestSearchCommand_HelpShowsSubcommands(t *testing.T) {
	// Root search help must advertise the corpus subcommands.
	t.Run("root shows code and commits", func(t *testing.T) {
		cmd := NewRootCmd()
		cmd.SetArgs([]string{"search", "--help"})
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("search --help: %v", err)
		}
		got := out.String()
		for _, want := range []string{"code", "commits"} {
			if !strings.Contains(got, want) {
				t.Errorf("expected %q in search --help output, got:\n%s", want, got)
			}
		}
	})

	// Each corpus subcommand must advertise query/index/clusters.
	for _, corpus := range []string{"code", "commits"} {
		corpus := corpus
		t.Run(corpus+" shows query, index, clusters", func(t *testing.T) {
			cmd := NewRootCmd()
			cmd.SetArgs([]string{"search", corpus, "--help"})
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("search %s --help: %v", corpus, err)
			}
			got := out.String()
			for _, want := range []string{"query", "index", "clusters"} {
				if !strings.Contains(got, want) {
					t.Errorf("expected %q in search %s --help output, got:\n%s", want, corpus, got)
				}
			}
		})
	}
}
