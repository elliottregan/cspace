package features

import (
	"strings"
	"testing"
)

func TestResolveEmpty(t *testing.T) {
	got, err := Resolve(nil)
	if err != nil || got != nil {
		t.Fatalf("got %v / %v", got, err)
	}
}

func TestResolveSupported(t *testing.T) {
	in := map[string]any{
		"ghcr.io/devcontainers/features/node:1":   map[string]any{"version": "20"},
		"ghcr.io/devcontainers/features/python:1": map[string]any{},
	}
	got, err := Resolve(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	// Sorted by ID.
	if got[0].ID != "ghcr.io/devcontainers/features/node:1" {
		t.Fatalf("first=%q", got[0].ID)
	}
	if got[0].Script != "/opt/cspace/features/node.sh" {
		t.Fatalf("script=%q", got[0].Script)
	}
	if got[0].Args["version"] != "20" {
		t.Fatalf("args=%v", got[0].Args)
	}
}

func TestResolveUnsupported(t *testing.T) {
	in := map[string]any{"ghcr.io/devcontainers/features/dotnet:1": map[string]any{}}
	_, err := Resolve(in)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "dotnet") {
		t.Fatalf("error missing feature ID: %v", err)
	}
}
