package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/registry"
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

func TestDownRefusesAnUnsafeName(t *testing.T) {
	cmd := newDownCmd()
	cmd.SetArgs([]string{"../../etc"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("down accepted a traversal-shaped name")
	}
	if !strings.Contains(err.Error(), "sandbox name") {
		t.Errorf("error = %v, want it to name the sandbox name", err)
	}
}

// TestDownAllSkipsALegacyNameAndContinues covers the --all path's
// skip-and-continue behavior added alongside validateSandboxName: a single
// invalid legacy registry entry (one that predates the shape check) must not
// abort the whole batch the way the single-name form does. It's the only
// entry in the registry, so once it's filtered out the teardown loop makes
// no substrate calls at all — this stays hermetic like the rest of the
// package's tests.
func TestDownAllSkipsALegacyNameAndContinues(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CSPACE_PROJECT", "demo")

	regPath, err := registry.DefaultPath()
	if err != nil {
		t.Fatalf("registry.DefaultPath: %v", err)
	}
	r := &registry.Registry{Path: regPath}
	if err := r.Register(registry.Entry{
		Project: "demo", Name: "dotted.name", IP: "192.168.64.10", StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed registry: %v", err)
	}

	cmd := newDownCmd()
	cmd.SetArgs([]string{"--all"})
	var stderr bytes.Buffer
	cmd.SetOut(io.Discard)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("down --all returned an error for a batch containing only an invalid name: %v", err)
	}

	if !strings.Contains(stderr.String(), "[cspace] skipping") {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), "[cspace] skipping")
	}
	if !strings.Contains(stderr.String(), "dotted.name") {
		t.Errorf("stderr = %q, want it to name the skipped entry %q", stderr.String(), "dotted.name")
	}
}
