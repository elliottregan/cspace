package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Corpora["code"].MaxBytes != 204800 {
		t.Errorf("expected MaxBytes 204800, got %d", c.Corpora["code"].MaxBytes)
	}
	if c.Sidecars.QdrantURL == "" {
		t.Errorf("expected QdrantURL to be defaulted, got empty")
	}
	if c.Index.LockPath == "" {
		t.Errorf("expected LockPath default")
	}
	if !c.Corpora["code"].Enabled {
		t.Errorf("expected code corpus enabled by default")
	}
	if len(c.Corpora["code"].Excludes) == 0 {
		t.Errorf("expected default excludes to be non-empty")
	}
}

func TestLoad_ProjectOverride(t *testing.T) {
	dir := t.TempDir()
	override := []byte(`
corpora:
  code:
    max_bytes: 50000
    enabled: false
`)
	if err := os.WriteFile(filepath.Join(dir, "search.yaml"), override, 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Corpora["code"].MaxBytes != 50000 {
		t.Errorf("expected override 50000, got %d", c.Corpora["code"].MaxBytes)
	}
	if c.Corpora["code"].Enabled {
		t.Errorf("expected override Enabled=false")
	}
	// Non-overridden fields still come from defaults.
	if c.Sidecars.QdrantURL == "" {
		t.Errorf("expected defaulted QdrantURL to survive partial override, got empty")
	}
}
