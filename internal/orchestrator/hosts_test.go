package orchestrator

import (
	"strings"
	"testing"
)

func TestRenderHosts(t *testing.T) {
	ips := map[string]string{
		"backend":   "192.168.64.41",
		"dashboard": "192.168.64.42",
		"workspace": "192.168.64.40",
	}
	got := renderHosts(ips)
	for _, want := range []string{
		"192.168.64.41 backend",
		"192.168.64.42 dashboard",
		"192.168.64.40 workspace",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(got, hostsMarkerStart) || !strings.Contains(got, hostsMarkerEnd) {
		t.Fatalf("markers missing:\n%s", got)
	}
}

func TestRenderHostsDeterministicOrder(t *testing.T) {
	// Sorted by name, regardless of insertion order.
	ips := map[string]string{"zebra": "1", "alpha": "2", "mike": "3"}
	got := renderHosts(ips)
	idx := func(name string) int { return strings.Index(got, name+"\n") }
	if idx("alpha") >= idx("mike") || idx("mike") >= idx("zebra") {
		t.Fatalf("not sorted:\n%s", got)
	}
}

func TestRenderHostsEmpty(t *testing.T) {
	got := renderHosts(map[string]string{})
	if !strings.Contains(got, hostsMarkerStart) || !strings.Contains(got, hostsMarkerEnd) {
		t.Fatalf("markers missing on empty input:\n%s", got)
	}
}
