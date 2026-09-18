package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// tuiSubcommand finds the registered "tui" subcommand on a fresh root, the
// same way TestRootRegistersTui (cmd_tui_test.go) confirms it exists.
func tuiSubcommand(t *testing.T, root *cobra.Command) *cobra.Command {
	t.Helper()
	for _, c := range root.Commands() {
		if c.Name() == "tui" {
			return c
		}
	}
	t.Fatal("root does not register the tui command")
	return nil
}

// runRootPreRun drives root's PersistentPreRunE the way cobra's executor
// does for a subcommand that declares none of its own: called with the
// subcommand as cmd (so cmd.Name() inside the hook sees "tui"), not the root
// itself.
func runRootPreRun(t *testing.T, root, sub *cobra.Command) error {
	t.Helper()
	if root.PersistentPreRunE == nil {
		t.Fatal("root has no PersistentPreRunE to test")
	}
	return root.PersistentPreRunE(sub, nil)
}

// TestRootPreRunTuiTolerateNoProject covers the brief/review finding: `cspace
// tui` run from a directory with no project at all (no .git found walking
// up) must proceed with cfg == nil rather than fail — the dashboard spans
// every project on the host and does not need one of its own.
func TestRootPreRunTuiTolerateNoProject(t *testing.T) {
	withCfg(t, nil)
	t.Setenv("CSPACE_SANDBOX_NAME", "") // force the host-side discovery path
	dir := t.TempDir()                  // outside the repo; no parent .git to find
	t.Chdir(dir)

	root := NewRootCmd()
	sub := tuiSubcommand(t, root)

	if err := runRootPreRun(t, root, sub); err != nil {
		t.Fatalf("PersistentPreRunE = %v, want nil outside any project", err)
	}
	if cfg != nil {
		t.Errorf("cfg = %+v, want nil outside any project", cfg)
	}
}

// TestRootPreRunTuiReportsMalformedConfig covers the other half: a directory
// that IS a project (has .git) but whose .cspace.json fails to parse must
// surface that error rather than being swallowed into the same nil-cfg
// fallback as "no project here at all".
func TestRootPreRunTuiReportsMalformedConfig(t *testing.T) {
	withCfg(t, nil)
	t.Setenv("CSPACE_SANDBOX_NAME", "")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".cspace.json"), []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	root := NewRootCmd()
	sub := tuiSubcommand(t, root)

	err := runRootPreRun(t, root, sub)
	if err == nil {
		t.Fatal("PersistentPreRunE = nil, want an error for malformed .cspace.json")
	}
	if !strings.Contains(err.Error(), "parsing .cspace.json") {
		t.Errorf("error = %q, want it to mention the parse failure", err.Error())
	}
}
