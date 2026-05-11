package v2

import (
	"context"
	"strings"
	"testing"
)

func TestSubsetRejectsNetworks(t *testing.T) {
	_, err := Parse(context.Background(), "testdata/with_networks.yml")
	if err == nil || !strings.Contains(err.Error(), "networks") {
		t.Fatalf("want networks-rejection error, got %v", err)
	}
}

func TestSubsetWarnsOnCapAdd(t *testing.T) {
	p, err := Parse(context.Background(), "testdata/with_capadd.yml")
	if err != nil {
		t.Fatalf("cap_add should parse with a warning, got error: %v", err)
	}
	if len(p.Warnings) == 0 {
		t.Fatal("expected a warning about cap_add, got none")
	}
	found := false
	for _, w := range p.Warnings {
		if strings.Contains(w, "cap_add") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("warnings did not mention cap_add: %v", p.Warnings)
	}
}

func TestSubsetRejectsPrivileged(t *testing.T) {
	_, err := Parse(context.Background(), "testdata/with_privileged.yml")
	if err == nil || !strings.Contains(err.Error(), "privileged") {
		t.Fatalf("want privileged-rejection error, got %v", err)
	}
}

func TestSubsetRejectsProfiles(t *testing.T) {
	_, err := Parse(context.Background(), "testdata/with_profile.yml")
	if err == nil || !strings.Contains(err.Error(), "profiles") {
		t.Fatalf("want profiles-rejection error, got %v", err)
	}
}

func TestSubsetRejectsLinks(t *testing.T) {
	_, err := Parse(context.Background(), "testdata/with_links.yml")
	if err == nil || !strings.Contains(err.Error(), "links") {
		t.Fatalf("want links-rejection error, got %v", err)
	}
}

func TestSubsetRejectsNetworkMode(t *testing.T) {
	_, err := Parse(context.Background(), "testdata/with_networkmode.yml")
	if err == nil || !strings.Contains(err.Error(), "network_mode") {
		t.Fatalf("want network_mode-rejection error, got %v", err)
	}
}

// Sanity: existing fixtures still parse cleanly.
func TestSubsetMinimalStillPasses(t *testing.T) {
	if _, err := Parse(context.Background(), "testdata/minimal.yml"); err != nil {
		t.Fatalf("minimal regressed: %v", err)
	}
	if _, err := Parse(context.Background(), "testdata/with_healthcheck.yml"); err != nil {
		t.Fatalf("with_healthcheck regressed: %v", err)
	}
}
