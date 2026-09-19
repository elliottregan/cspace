package cli

import (
	"bytes"
	"context"
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

// noSubstrate is the injected substrate: teardown is best-effort and has no
// error return, so a no-op is a faithful stand-in for "the container was
// already gone". It exists so `go test ./internal/cli/` never runs
// `container rm --force cspace-demo-mercury` against whatever a developer
// happens to have booted.
type noSubstrate struct{}

func (noSubstrate) Stop(context.Context, string) error { return nil }
func (noSubstrate) ListVolumes(context.Context, string) ([]string, error) {
	return nil, nil
}
func (noSubstrate) RemoveVolume(context.Context, string) error { return nil }

// noSidecars silences the two stopBrowserSidecar calls for one test. Those
// run `container stop` and `container rm` by name and are not adapter
// methods, so they need their own seam.
func noSidecars(t *testing.T) {
	t.Helper()
	prev := stopSidecarContainer
	stopSidecarContainer = func(context.Context, string) {}
	t.Cleanup(func() { stopSidecarContainer = prev })
}

func TestTeardownKeepsTheRegistryEntryWithKeepState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // teardownSandbox resolves $HOME itself; see below
	noSidecars(t)
	reg := &registry.Registry{Path: filepath.Join(home, "sandbox-registry.json")}
	if err := reg.Register(registry.Entry{Project: "demo", Name: "mercury", State: "ready"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	var out bytes.Buffer

	teardownSandbox(context.Background(), noSubstrate{}, reg, "demo", "mercury", &out, false /* wipeState */)

	e, err := reg.Lookup("demo", "mercury")
	if err != nil {
		t.Fatalf("--keep-state removed the registry entry: %v", err)
	}
	if e.State != "stopped" {
		t.Errorf("state = %q, want stopped", e.State)
	}
}

func TestTeardownUnregistersWithoutKeepState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // this one reaches wipeSandboxState's RemoveAlls
	noSidecars(t)
	reg := &registry.Registry{Path: filepath.Join(home, "sandbox-registry.json")}
	if err := reg.Register(registry.Entry{Project: "demo", Name: "mercury", State: "ready"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	var out bytes.Buffer

	teardownSandbox(context.Background(), noSubstrate{}, reg, "demo", "mercury", &out, true /* wipeState */)

	if _, err := reg.Lookup("demo", "mercury"); err == nil {
		t.Error("the default teardown left the registry entry behind")
	}
}

// TestDownAllSkipsAKeptEntry — `--all` built its name list from every
// registry entry in the project, which after --keep-state includes the
// stopped ones. Running plain `cspace down --all` then reached
// wipeSandboxState for a sandbox with no container to tear down, deleting
// the clone, sessions and volumes the operator deliberately kept. The kept
// entry is skipped and named instead; nothing calls the substrate, so this
// stays hermetic.
func TestDownAllSkipsAKeptEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CSPACE_PROJECT", "demo")

	regPath, err := registry.DefaultPath()
	if err != nil {
		t.Fatalf("registry.DefaultPath: %v", err)
	}
	r := &registry.Registry{Path: regPath}
	if err := r.Register(registry.Entry{
		Project: "demo", Name: "mercury", State: registry.StateStopped, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed registry: %v", err)
	}

	// The state the flag was for: a clone that must survive a later --all.
	clone := filepath.Join(home, ".cspace", "clones", "demo", "mercury")
	if err := os.MkdirAll(clone, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := newDownCmd()
	cmd.SetArgs([]string{"--all"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("down --all: %v", err)
	}

	if !strings.Contains(stdout.String(), "kept: mercury (stopped;") {
		t.Errorf("stdout = %q, want it to name the kept sandbox", stdout.String())
	}
	if !strings.Contains(stdout.String(), "nothing to tear down") {
		t.Errorf("stdout = %q, want a closing line when every name is filtered out", stdout.String())
	}
	if _, err := os.Stat(clone); err != nil {
		t.Errorf("down --all wiped the clone of a kept sandbox: %v", err)
	}
	if _, err := r.Lookup("demo", "mercury"); err != nil {
		t.Errorf("down --all purged the kept registry entry: %v", err)
	}
}

// TestDownASingleKeptSandboxStillPurges — skipping is a property of the
// batch, not of the state. Naming a kept sandbox is how an operator
// deliberately reclaims it, and that has to keep working.
func TestDownASingleKeptSandboxStillPurges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	noSidecars(t)
	reg := &registry.Registry{Path: filepath.Join(home, "sandbox-registry.json")}
	if err := reg.Register(registry.Entry{
		Project: "demo", Name: "mercury", State: registry.StateStopped,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	clone := filepath.Join(home, ".cspace", "clones", "demo", "mercury")
	if err := os.MkdirAll(clone, 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer

	teardownSandbox(context.Background(), noSubstrate{}, reg, "demo", "mercury", &out, true /* wipeState */)

	if _, err := reg.Lookup("demo", "mercury"); err == nil {
		t.Error("purging a kept sandbox left its registry entry behind")
	}
	if _, err := os.Stat(clone); !os.IsNotExist(err) {
		t.Errorf("purging a kept sandbox left its clone behind: %v", err)
	}
}
