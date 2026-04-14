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
