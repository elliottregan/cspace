package provision

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReadTeleportManifest(t *testing.T) {
	dir := t.TempDir()
	m := teleportManifest{
		Source:       "mercury",
		Target:       "mars",
		SessionID:    "abc-123",
		CreatedAt:    "2026-04-14T00:00:00Z",
		SourceHead:   "deadbeef",
		SourceBranch: "main",
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
