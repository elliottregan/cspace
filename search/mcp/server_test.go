package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elliottregan/cspace/search/config"

	mcpSDK "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServer_RegistersAllTools(t *testing.T) {
	srv := mcpSDK.NewServer(&mcpSDK.Implementation{Name: "test", Version: "0"}, nil)
	s := &Server{ProjectRoot: ".", Config: &config.Config{
		Corpora: map[string]config.CorpusConfig{
			"code": {Enabled: true},
		},
	}}
	// Register must not panic.
	s.Register(srv)
}

func TestServer_HandleStatus_NoStatusFile(t *testing.T) {
	dir := t.TempDir()
	s := &Server{
		ProjectRoot: dir,
		Config: &config.Config{
			Corpora: map[string]config.CorpusConfig{
				"code":    {Enabled: true},
				"commits": {Enabled: true},
				"context": {Enabled: false},
				"issues":  {Enabled: false},
			},
		},
	}
	result, out, err := s.handleStatus(nil)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if out.Current != nil {
		t.Error("expected no current run")
	}
	// context and issues should be disabled.
	if out.Corpora["context"].State != "disabled" {
		t.Errorf("expected context=disabled, got %q", out.Corpora["context"].State)
	}
	if out.Corpora["issues"].State != "disabled" {
		t.Errorf("expected issues=disabled, got %q", out.Corpora["issues"].State)
	}
	// code and commits should be unknown (no status file).
	if out.Corpora["code"].State != "unknown" {
		t.Errorf("expected code=unknown, got %q", out.Corpora["code"].State)
	}
}

func TestServer_HandleStatus_WithStatusFile(t *testing.T) {
	dir := t.TempDir()
	cspaceDir := filepath.Join(dir, ".cspace")
	if err := os.MkdirAll(cspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	statusJSON := `{
		"updated_at": "2026-04-22T03:00:00Z",
		"current": null,
		"last": {
			"code": {"state": "completed", "finished_at": "2026-04-22T03:00:00Z", "indexed_count": 100},
			"commits": {"state": "failed", "finished_at": "2026-04-22T03:00:00Z", "error": "embed: connection refused"}
		}
	}`
	if err := os.WriteFile(filepath.Join(cspaceDir, "search-index-status.json"), []byte(statusJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Server{
		ProjectRoot: dir,
		Config: &config.Config{
			Corpora: map[string]config.CorpusConfig{
				"code":    {Enabled: true},
				"commits": {Enabled: true},
				"context": {Enabled: false},
				"issues":  {Enabled: false},
			},
			Sidecars: config.Sidecars{
				QdrantURL: "http://localhost:6333", // won't actually connect in test
			},
		},
	}
	_, out, err := s.handleStatus(nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Corpora["code"].State != "completed" {
		t.Errorf("expected code=completed, got %q", out.Corpora["code"].State)
	}
	if out.Corpora["code"].IndexedCount != 100 {
		t.Errorf("expected indexed_count=100, got %d", out.Corpora["code"].IndexedCount)
	}
	if out.Corpora["commits"].State != "failed" {
		t.Errorf("expected commits=failed, got %q", out.Corpora["commits"].State)
	}
	if out.Corpora["commits"].Error != "embed: connection refused" {
		t.Errorf("unexpected error: %q", out.Corpora["commits"].Error)
	}
}
