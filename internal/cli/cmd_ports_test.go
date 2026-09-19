package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
)

// TestPortsSkipsProbingAStoppedSandbox — `cspace down --keep-state` leaves
// a registry entry with State == registry.StateStopped and no IP. Before
// this guard, probePorts got net.JoinHostPort("", port) => ":<port>", which
// net.DialTimeout happily connects to on the OPERATOR'S OWN loopback — so
// `cspace ports <kept-sandbox>` reported the host's own listening ports as
// the sandbox's. Assert both the message and that nothing gets dialled.
func TestPortsSkipsProbingAStoppedSandbox(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	regPath, err := registry.DefaultPath()
	if err != nil {
		t.Fatalf("registry.DefaultPath: %v", err)
	}
	r := &registry.Registry{Path: regPath}
	if err := r.Register(registry.Entry{
		Project: "demo", Name: "kept", State: registry.StateStopped, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed kept entry: %v", err)
	}

	orig := probePortsFn
	t.Cleanup(func() { probePortsFn = orig })
	probePortsFn = func(host string, ports []int, timeout time.Duration) []int {
		t.Fatal("probePorts was called for a stopped, kept sandbox — it would dial the operator's own loopback")
		return nil
	}

	var out bytes.Buffer
	if err := runPorts(&out, "demo", "kept"); err != nil {
		t.Fatalf("runPorts: %v", err)
	}

	want := "kept is stopped (kept with --keep-state); no ports to probe — run cspace up kept to boot it"
	if !strings.Contains(out.String(), want) {
		t.Errorf("output = %q, want it to contain %q", out.String(), want)
	}
}

// TestPortsSkipsProbingAnEmptyIPEvenWithoutStoppedState guards the other
// half of the condition: an entry with no IP for any reason (not just the
// keep-state path) must never reach probePorts either, since
// net.JoinHostPort("", port) is never a sandbox address.
func TestPortsSkipsProbingAnEmptyIPEvenWithoutStoppedState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	regPath, err := registry.DefaultPath()
	if err != nil {
		t.Fatalf("registry.DefaultPath: %v", err)
	}
	r := &registry.Registry{Path: regPath}
	if err := r.Register(registry.Entry{
		Project: "demo", Name: "ipless", State: registry.StateReady, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed ipless entry: %v", err)
	}

	orig := probePortsFn
	t.Cleanup(func() { probePortsFn = orig })
	probePortsFn = func(host string, ports []int, timeout time.Duration) []int {
		t.Fatal("probePorts was called for an entry with no IP")
		return nil
	}

	var out bytes.Buffer
	if err := runPorts(&out, "demo", "ipless"); err != nil {
		t.Fatalf("runPorts: %v", err)
	}
	if !strings.Contains(out.String(), "no ports to probe") {
		t.Errorf("output = %q, want the no-ports-to-probe message", out.String())
	}
}
