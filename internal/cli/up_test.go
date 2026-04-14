package cli

import (
	"testing"
)

func TestUpCmdHasTeleportFromFlag(t *testing.T) {
	cmd := newUpCmd()
	flag := cmd.Flag("teleport-from")
	if flag == nil {
		t.Fatal("expected --teleport-from flag to be registered")
	}
	if flag.Value.String() != "" {
		t.Errorf("expected default empty, got %q", flag.Value.String())
	}
}

func TestUpCmdTeleportFromConflictsWithPrompt(t *testing.T) {
	cmd := newUpCmd()
	cmd.SetArgs([]string{"mars", "--teleport-from", "/tmp/abc", "--prompt", "hi"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --teleport-from is combined with --prompt")
	}
}

func TestUpCmdTeleportFromConflictsWithPromptFile(t *testing.T) {
	cmd := newUpCmd()
	cmd.SetArgs([]string{"mars", "--teleport-from", "/tmp/abc", "--prompt-file", "/tmp/p.txt"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when --teleport-from is combined with --prompt-file")
	}
}

func TestUpCmdTeleportFromConflictsWithNoClaude(t *testing.T) {
	cmd := newUpCmd()
	cmd.SetArgs([]string{"mars", "--teleport-from", "/tmp/abc", "--no-claude"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when --teleport-from is combined with --no-claude")
	}
}

func TestUpCmdTeleportFromMissingDir(t *testing.T) {
	cmd := newUpCmd()
	cmd.SetArgs([]string{"mars", "--teleport-from", "/nonexistent/teleport/dir/xyz"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --teleport-from path does not exist")
	}
	// Defensive: should not have reached provisioning.
}
