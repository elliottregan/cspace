package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elliottregan/cspace/internal/registry"
)

// TestGatewayDNSProbeDegradesGracefully guards the container-facing gateway
// DNS check (192.168.64.1:5354): the vmnet gateway address doesn't exist
// until a container has booted, so a dial/answer failure here must degrade
// to ProbeWarn — never ProbeFail, which would fail `doctor` in CI where no
// vmnet bridge is up at all.
func TestGatewayDNSProbeDegradesGracefully(t *testing.T) {
	c := probeGatewayDNS()
	if c.Title == "" {
		t.Fatal("probe check needs a title")
	}
	// No vmnet bridge in CI => must warn, never hard-fail (which would fail doctor).
	if c.Status == ProbeFail {
		t.Errorf("gateway DNS should degrade to warn, got fail: %+v", c)
	}
}

// TestFirstAliveEntryNoEntries exercises probeInContainerDNS's "nothing to
// test" precondition in isolation from the real on-disk registry: with no
// entries at all, there's nothing alive to pick.
func TestFirstAliveEntryNoEntries(t *testing.T) {
	_, ok := firstAliveEntry(context.Background(), nil)
	if ok {
		t.Fatal("expected no alive entry from an empty registry")
	}
}

// TestFirstAliveEntrySkipsBootingAndIPless confirms entries still booting
// (no IP yet, or State == "starting") are skipped rather than treated as
// alive — a doctor probe against a half-booted sandbox would just be noise.
func TestFirstAliveEntrySkipsBootingAndIPless(t *testing.T) {
	entries := []registry.Entry{
		{Name: "a", Project: "p", IP: "", State: "ready"},
		{Name: "b", Project: "p", IP: "10.0.0.2", State: "starting"},
	}
	_, ok := firstAliveEntry(context.Background(), entries)
	if ok {
		t.Fatal("expected no alive entry: one has no IP, the other is still starting")
	}
}

// TestProbeSandboxesSeparatesKeptFromDead — a `cspace down --keep-state`
// entry (State == registry.StateStopped) has no container, same as a truly
// stale one, but for the opposite reason: it's kept on purpose. Before this
// fix ProbeSandboxes folded it into "dead registry entries" and doctor
// pointed at `cspace registry prune`, which now deliberately leaves
// state=stopped entries alone — a warning that could never be resolved the
// way it told the operator to resolve it.
func TestProbeSandboxesSeparatesKeptFromDead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	regPath, err := registry.DefaultPath()
	if err != nil {
		t.Fatalf("registry.DefaultPath: %v", err)
	}
	r := &registry.Registry{Path: regPath}
	if err := r.Register(registry.Entry{
		Project: "demo", Name: "kept", State: registry.StateStopped,
	}); err != nil {
		t.Fatalf("seed kept entry: %v", err)
	}
	if err := r.Register(registry.Entry{
		Project: "demo", Name: "stale", State: registry.StateReady,
	}); err != nil {
		t.Fatalf("seed stale entry: %v", err)
	}

	result := ProbeSandboxes(context.Background())

	var deadCheck, keptCheck *ProbeCheck
	for i := range result.Checks {
		c := &result.Checks[i]
		switch {
		case strings.Contains(c.Title, "dead registry entries"):
			deadCheck = c
		case strings.Contains(c.Title, "kept (stopped)"):
			keptCheck = c
		}
	}

	if deadCheck == nil {
		t.Fatal("expected a dead-registered check")
	}
	if !strings.Contains(deadCheck.Title, "demo:stale") {
		t.Errorf("dead check = %+v, want it to name demo:stale", *deadCheck)
	}
	if strings.Contains(deadCheck.Title, "demo:kept") {
		t.Errorf("dead check = %+v, should not include the kept entry", *deadCheck)
	}

	if keptCheck == nil {
		t.Fatal("expected a kept-(stopped) check")
	}
	if keptCheck.Status != ProbePass {
		t.Errorf("kept check status = %v, want ProbePass (informational, not a warning)", keptCheck.Status)
	}
	if !strings.Contains(keptCheck.Title, "demo:kept") {
		t.Errorf("kept check = %+v, want it to name demo:kept", *keptCheck)
	}
}

// TestCheckInContainerDNSOnceMapsOutcomes verifies the single-attempt exec
// wrapper maps resolve/no-resolve/exec-error to Pass/Warn/Warn respectively
// — a dead in-container resolver is advisory (Warn), never a doctor Fail.
func TestCheckInContainerDNSOnceMapsOutcomes(t *testing.T) {
	resolves := func(ctx context.Context, container string, argv ...string) ([]byte, error) {
		return []byte("10.0.0.5   sandbox.project.cspace.test\n"), nil
	}
	c := checkInContainerDNSOnce(context.Background(), resolves, "cspace-p-a", "a.p.cspace.test")
	if c.Status != ProbePass {
		t.Errorf("expected Pass on successful resolution, got %+v", c)
	}

	empty := func(ctx context.Context, container string, argv ...string) ([]byte, error) {
		return []byte(""), nil
	}
	c = checkInContainerDNSOnce(context.Background(), empty, "cspace-p-a", "a.p.cspace.test")
	if c.Status != ProbeWarn {
		t.Errorf("expected Warn on empty getent output, got %+v", c)
	}

	failing := func(ctx context.Context, container string, argv ...string) ([]byte, error) {
		return nil, errors.New("exec: container not found")
	}
	c = checkInContainerDNSOnce(context.Background(), failing, "cspace-p-a", "a.p.cspace.test")
	if c.Status != ProbeWarn {
		t.Errorf("expected Warn on exec error (never Fail), got %+v", c)
	}
	if c.Status == ProbeFail {
		t.Errorf("in-container DNS probe should never hard-fail, got: %+v", c)
	}
}

// TestInContainerDNSProbeNeverFails is a light end-to-end smoke test against
// probeInContainerDNS() itself. It doesn't assume anything about the host's
// real ~/.cspace/sandbox-registry.json (which may or may not have live
// sandboxes registered from other projects) — only that the probe never
// reports ProbeFail, matching the "advisory, not a doctor gate" contract.
func TestInContainerDNSProbeNeverFails(t *testing.T) {
	c := probeInContainerDNS()
	if c.Title == "" {
		t.Fatal("probe check needs a title")
	}
	if c.Status == ProbeFail {
		t.Errorf("in-container DNS probe should never hard-fail, got: %+v", c)
	}
}
