package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestSearchCommand_HelpShowsSubcommands(t *testing.T) {
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"search", "--help"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("search --help: %v", err)
	}
	got := out.String()
	for _, want := range []string{"clusters", "index"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in help output, got:\n%s", want, got)
		}
	}
}
