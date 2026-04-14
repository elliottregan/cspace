package provision

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elliottregan/cspace/internal/config"
)

func TestReadTeleportManifest(t *testing.T) {
	dir := t.TempDir()
	m := teleportManifest{
		Source:          "mercury",
		Target:          "mars",
		SessionID:       "abc-123",
		CreatedAt:       "2026-04-14T00:00:00Z",
		SourceHead:      "deadbeef",
		SourceBranch:    "main",
		SourceRemoteURL: "https://github.com/example/repo.git",
	}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0644); err != nil {
		t.Fatal(err)
	}

	got, err := readTeleportManifest(dir)
	if err != nil {
		t.Fatalf("readTeleportManifest: %v", err)
	}
	if got.SessionID != "abc-123" {
		t.Errorf("SessionID = %q, want abc-123", got.SessionID)
	}
	if got.Target != "mars" {
		t.Errorf("Target = %q, want mars", got.Target)
	}
	if got.SourceRemoteURL != "https://github.com/example/repo.git" {
		t.Errorf("SourceRemoteURL = %q, want https://github.com/example/repo.git", got.SourceRemoteURL)
	}
}

func TestReadTeleportManifestRejectsMissing(t *testing.T) {
	_, err := readTeleportManifest(t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing manifest.json")
	}
}

func TestReadTeleportManifestRejectsMissingSessionID(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"target":"mars"}`), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := readTeleportManifest(dir)
	if err == nil {
		t.Fatal("expected error when session_id is missing")
	}
}

// Exercises the TeleportRun target-vs-manifest mismatch guard without touching docker.
// The mismatch check runs before any external calls, so we can verify the exact error.
func TestTeleportRunRejectsManifestTargetMismatch(t *testing.T) {
	dir := t.TempDir()

	// Write a manifest whose target is different from the requested instance name.
	manifest := teleportManifest{
		Source:       "mercury",
		Target:       "mars",
		SessionID:    "abc-123",
		SourceBranch: "main",
	}
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	// Satisfy the bundle+transcript existence checks.
	if err := os.WriteFile(filepath.Join(dir, "workspace.bundle"), []byte("fake"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	// A minimally valid cfg is enough — TeleportRun errors out before calling docker.
	cfg := &config.Config{}

	err := TeleportRun(TeleportParams{
		Name:         "venus", // != "mars"
		TeleportFrom: dir,
		Cfg:          cfg,
	})
	if err == nil {
		t.Fatal("expected error when manifest target mismatches requested name")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("expected 'does not match' error, got: %v", err)
	}
}
