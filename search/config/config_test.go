package config

import (
	"errors"
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
	if !c.Corpora["commits"].Enabled {
		t.Errorf("expected commits corpus enabled by default")
	}
	if c.Corpora["context"].Enabled {
		t.Errorf("expected context corpus DISABLED by default")
	}
	if c.Corpora["issues"].Enabled {
		t.Errorf("expected issues corpus DISABLED by default")
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

// Flipping a disabled-by-default corpus back on via search.yaml must be
// effective — this is the documented opt-in path for issues and context.
func TestLoad_OptInDisabledCorpus(t *testing.T) {
	dir := t.TempDir()
	override := []byte(`
corpora:
  issues:
    enabled: true
`)
	if err := os.WriteFile(filepath.Join(dir, "search.yaml"), override, 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !c.Corpora["issues"].Enabled {
		t.Errorf("expected issues corpus to opt in via override")
	}
	if c.Corpora["issues"].Limit != 500 {
		t.Errorf("expected defaulted limit 500 to survive partial override, got %d", c.Corpora["issues"].Limit)
	}
}

func TestBuildWithConfig_DisabledCorpusReturnsSentinel(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir) // defaults: issues + context disabled
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, id := range []string{"issues", "context"} {
		rt, err := BuildWithConfig(dir, id, cfg)
		if err == nil {
			t.Errorf("%s: expected error, got runtime %+v", id, rt)
			continue
		}
		if !errors.Is(err, ErrCorpusDisabled) {
			t.Errorf("%s: expected ErrCorpusDisabled, got %v", id, err)
		}
	}
}

func TestBuildWithConfig_EnabledCorpusBuildsOK(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, id := range []string{"code", "commits"} {
		rt, err := BuildWithConfig(dir, id, cfg)
		if err != nil {
			t.Errorf("%s: expected build to succeed, got %v", id, err)
			continue
		}
		if rt == nil || rt.Corpus == nil {
			t.Errorf("%s: expected non-nil runtime + corpus", id)
		}
	}
}
