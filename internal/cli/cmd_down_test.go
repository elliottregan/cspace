package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/elliottregan/cspace/internal/control"
)

// TestRemoveControlPlaneDirDeletesTheAttachBookkeeping — cspace down has to
// reclaim this alongside sessions/clone/volumes, or a stale lock/record from
// a torn-down sandbox sits under ~/.cspace/controlplane/ forever (the
// directory is keyed by project+sandbox name, so the next `up` under the
// same name would inherit it).
func TestRemoveControlPlaneDirDeletesTheAttachBookkeeping(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := control.ControlPlaneDir(home, "proj", "sandbox-down-test")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "attach.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	removeControlPlaneDir(&buf, "proj", "sandbox-down-test")

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("control-plane dir survived removeControlPlaneDir(): %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("unexpected warning output: %q", buf.String())
	}
}

// TestRemoveControlPlaneDirIgnoresAMissingDir — cspace down runs this
// unconditionally; a sandbox that never had an attach (no-tmux fallback, or
// simply never attached to) has no control-plane dir at all, and that must
// not produce a warning.
func TestRemoveControlPlaneDirIgnoresAMissingDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var buf bytes.Buffer
	removeControlPlaneDir(&buf, "proj", "never-attached")

	if buf.Len() != 0 {
		t.Errorf("unexpected warning output for a directory that never existed: %q", buf.String())
	}
}
