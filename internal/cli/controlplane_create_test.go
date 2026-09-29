package cli

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elliottregan/cspace/internal/registry"
)

func TestControlPlaneNamerUsesProjectRegistryAndCLIValidation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	r := &registry.Registry{Path: filepath.Join(home, ".cspace", "sandbox-registry.json")}
	for _, entry := range []registry.Entry{
		{Project: "demo", Name: "mercury"},
		{Project: "other", Name: "venus"},
	} {
		if err := r.Register(entry); err != nil {
			t.Fatal(err)
		}
	}
	a := &cpActor{}
	name, err := a.SuggestName(context.Background(), "demo")
	if err != nil || name != "venus" {
		t.Fatalf("suggest = %q, %v; want venus", name, err)
	}
	if err := a.ValidateName("demo", "issue-42"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "browser", "../other", "has.dot", strings.Repeat("a", 64)} {
		if err := a.ValidateName("demo", name); err == nil {
			t.Errorf("ValidateName accepted %q", name)
		}
	}
}

func TestControlPlaneNamerExhaustionAndCancellation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	r := &registry.Registry{Path: filepath.Join(home, ".cspace", "sandbox-registry.json")}
	for _, name := range planetOrder {
		if err := r.Register(registry.Entry{Project: "demo", Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	a := &cpActor{}
	if name, err := a.SuggestName(context.Background(), "demo"); name != "" || err != nil {
		t.Fatalf("exhausted = %q, %v; want an empty suggestion for a custom name", name, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.SuggestName(ctx, "demo"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
