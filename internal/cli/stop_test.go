package cli

import (
	"strings"
	"testing"
)

func TestStopCmdExists(t *testing.T) {
	cmd := newStopCmd()
	if cmd.Use == "" {
		t.Fatal("expected newStopCmd to return a configured command")
	}
	if cmd.Short == "" {
		t.Error("expected Short description to be set")
	}
}

// TestStopCmdDescribesNonDestructiveSemantics locks in the defining
// characteristic of `cspace stop`: volumes must survive. If someone
// accidentally wires this to `compose.Run("down")`, the docstring and
// the Short description become a lie. Rather than spin up a whole
// mock compose harness for a one-line call, we anchor on the
// user-visible contract that the command's copy must promise
// non-destructive behavior.
func TestStopCmdDescribesNonDestructiveSemantics(t *testing.T) {
	cmd := newStopCmd()
	lower := strings.ToLower(cmd.Short)
	// The Short may legitimately mention "destroy" or "remove" in a
	// negated form ("without destroying"). What it must NOT do is promise
	// affirmative destructive behavior. Strip negated occurrences first.
	stripped := strings.ReplaceAll(lower, "without destroying", "")
	stripped = strings.ReplaceAll(stripped, "without removing", "")
	if strings.Contains(stripped, "destroy") || strings.Contains(stripped, "remove") {
		t.Errorf("Short description implies destructive behavior: %q", cmd.Short)
	}
	if !strings.Contains(lower, "volume") {
		t.Errorf("Short description should mention volume preservation: %q", cmd.Short)
	}
}

// TestStopCmdRequiresExactlyOneArg asserts that `cspace stop` takes a
// single instance name — unlike `cspace down` which supports --all and
// --everywhere. This anchors the "single instance only" scope.
func TestStopCmdRequiresExactlyOneArg(t *testing.T) {
	cmd := newStopCmd()
	// cobra.ExactArgs(1) is a function, not a sentinel — we verify by
	// calling it with different arg counts.
	for name, args := range map[string][]string{
		"zero args":  {},
		"two args":   {"a", "b"},
		"three args": {"a", "b", "c"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := cmd.Args(cmd, args); err == nil {
				t.Errorf("expected error for %d args, got nil", len(args))
			}
		})
	}
	if err := cmd.Args(cmd, []string{"mercury"}); err != nil {
		t.Errorf("expected no error for exactly 1 arg, got: %v", err)
	}

	// Ensure --all and --everywhere flags are NOT present (distinguishing
	// stop from down).
	if cmd.Flag("all") != nil {
		t.Error("cspace stop must not have --all flag (that would be destructive semantics)")
	}
	if cmd.Flag("everywhere") != nil {
		t.Error("cspace stop must not have --everywhere flag")
	}
}
