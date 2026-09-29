package applecontainer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func container15Fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "1.5.0", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// These fixtures were captured from Apple Container 1.5.0, with unrelated
// fields removed before saving. Keep the older fixtures in adapter_test.go:
// they document contracts that existing installations still depend on.
func TestAppleContainer15SystemStatus(t *testing.T) {
	running := string(container15Fixture(t, "system-status.txt"))
	if err := parseSystemStatus(running); err != nil {
		t.Fatalf("running 1.5 status: %v", err)
	}
	// Keep containers.running in the richer table to ensure only the actual
	// status row decides health. This stopped variant is synthetic.
	stopped := strings.Replace(running, "status              running", "status              stopped", 1)
	if stopped == running {
		t.Fatal("fixture has no expected status row")
	}
	if err := parseSystemStatus(stopped); err == nil {
		t.Fatal("stopped apiserver reported healthy")
	}
}

func TestAppleContainer15List(t *testing.T) {
	got, err := parseContainerList(string(container15Fixture(t, "list.json")))
	if err != nil {
		t.Fatal(err)
	}
	want := ContainerSummary{
		Name: "buildkit", Image: "ghcr.io/apple/container-builder-shim/builder:0.13.1",
		State: "running", IP: "192.168.64.4", CPUs: 2, MemoryB: 2147483648,
		Started: time.Date(2026, 9, 29, 19, 47, 2, 0, time.UTC),
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("list = %+v, want [%+v]", got, want)
	}
}

func TestAppleContainer15Inspect(t *testing.T) {
	got, err := ParseInspectIPv4(container15Fixture(t, "inspect.json"))
	if err != nil || got != "192.168.64.4" {
		t.Fatalf("inspect IP = %q, %v; want 192.168.64.4", got, err)
	}
}

func TestAppleContainer15NetworkGateway(t *testing.T) {
	got, err := ParseNetworkGateway(container15Fixture(t, "network-inspect.json"))
	if err != nil || got != "192.168.64.1" {
		t.Fatalf("gateway = %q, %v; want 192.168.64.1", got, err)
	}
}

func TestAppleContainer15Stats(t *testing.T) {
	got, err := parseContainerStats(string(container15Fixture(t, "stats.json")))
	if err != nil {
		t.Fatal(err)
	}
	want := ContainerStats{Name: "buildkit", MemoryUsedB: 1312485376, Processes: 20}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("stats = %+v, want [%+v]", got, want)
	}
}
