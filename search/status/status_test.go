package status

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriter_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	projectRoot := dir
	cspaceDir := filepath.Join(projectRoot, ".cspace")
	if err := os.MkdirAll(cspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}

	w, err := NewWriter(projectRoot)
	if err != nil {
		t.Fatal(err)
	}

	w.StartCorpus("code")

	// Status file should exist and be valid JSON.
	data, err := os.ReadFile(StatusPath(projectRoot))
	if err != nil {
		t.Fatal("status file not created:", err)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal("invalid JSON:", err)
	}
	if f.Current == nil {
		t.Fatal("expected current to be set")
	}
	if f.Current.Corpus != "code" {
		t.Errorf("expected corpus=code, got %q", f.Current.Corpus)
	}

	// No .tmp file should remain.
	if _, err := os.Stat(StatusPath(projectRoot) + ".tmp"); !os.IsNotExist(err) {
		t.Error("tmp file should not exist after write")
	}
}

func TestWriter_FinishCorpus(t *testing.T) {
	dir := t.TempDir()
	projectRoot := dir
	if err := os.MkdirAll(filepath.Join(projectRoot, ".cspace"), 0o755); err != nil {
		t.Fatal(err)
	}

	w, err := NewWriter(projectRoot)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now().Add(-50 * time.Millisecond) // simulate a run that took 50ms+
	w.StartCorpus("code")
	w.FinishCorpus("code", start, 42)

	f, err := Read(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if f.Current != nil {
		t.Error("expected current to be nil after finish")
	}
	cs, ok := f.Last["code"]
	if !ok {
		t.Fatal("expected 'code' in Last map")
	}
	if cs.State != "completed" {
		t.Errorf("expected state=completed, got %q", cs.State)
	}
	if cs.IndexedCount != 42 {
		t.Errorf("expected indexed_count=42, got %d", cs.IndexedCount)
	}
	if cs.DurationMS <= 0 {
		t.Errorf("expected positive duration_ms, got %d", cs.DurationMS)
	}
}

func TestWriter_FailCorpus(t *testing.T) {
	dir := t.TempDir()
	projectRoot := dir
	if err := os.MkdirAll(filepath.Join(projectRoot, ".cspace"), 0o755); err != nil {
		t.Fatal(err)
	}

	w, err := NewWriter(projectRoot)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	w.StartCorpus("commits")
	w.FailCorpus("commits", start, errors.New("embed batch 3: connection refused"))

	f, err := Read(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if f.Current != nil {
		t.Error("expected current to be nil after fail")
	}
	cs := f.Last["commits"]
	if cs.State != "failed" {
		t.Errorf("expected state=failed, got %q", cs.State)
	}
	if cs.Error != "embed batch 3: connection refused" {
		t.Errorf("unexpected error: %q", cs.Error)
	}
}

func TestWriter_DisableCorpus(t *testing.T) {
	dir := t.TempDir()
	projectRoot := dir
	if err := os.MkdirAll(filepath.Join(projectRoot, ".cspace"), 0o755); err != nil {
		t.Fatal(err)
	}

	w, err := NewWriter(projectRoot)
	if err != nil {
		t.Fatal(err)
	}

	w.DisableCorpus("context")

	f, err := Read(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	cs := f.Last["context"]
	if cs.State != "disabled" {
		t.Errorf("expected state=disabled, got %q", cs.State)
	}
}

func TestWriter_PreservesExistingState(t *testing.T) {
	dir := t.TempDir()
	projectRoot := dir
	if err := os.MkdirAll(filepath.Join(projectRoot, ".cspace"), 0o755); err != nil {
		t.Fatal(err)
	}

	// First writer records code as completed.
	w1, err := NewWriter(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	w1.FinishCorpus("code", time.Now(), 100)

	// Second writer records commits as completed — code should still be there.
	w2, err := NewWriter(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	w2.FinishCorpus("commits", time.Now(), 50)

	f, err := Read(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if f.Last["code"].State != "completed" {
		t.Error("code state was not preserved")
	}
	if f.Last["commits"].State != "completed" {
		t.Error("commits state was not recorded")
	}
}

func TestRead_MissingFile(t *testing.T) {
	f, err := Read(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if f != nil {
		t.Error("expected nil for missing file")
	}
}

// TestWriter_DisableCorpusDoesNotClobberExistingEntries is a regression test
// for PR #61 item #1: an outer writer whose in-memory snapshot is stale can
// clobber entries written by inner writers. The fix is single-use writers —
// each DisableCorpus call uses a fresh writer that re-reads on construction.
func TestWriter_DisableCorpusDoesNotClobberExistingEntries(t *testing.T) {
	dir := t.TempDir()
	projectRoot := dir
	if err := os.MkdirAll(filepath.Join(projectRoot, ".cspace"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Simulate runSearchIndex writing completed state for "code" and "commits"
	// via fresh single-use writers (as index.Run does internally).
	w1, err := NewWriter(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	w1.FinishCorpus("code", time.Now(), 100)

	w2, err := NewWriter(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	w2.FinishCorpus("commits", time.Now(), 50)

	// Now simulate the disabled-state writes for "context" and "issues" using
	// fresh writers (as the fixed runSearchIndex does for ErrCorpusDisabled).
	w3, err := NewWriter(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	w3.DisableCorpus("context")

	w4, err := NewWriter(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	w4.DisableCorpus("issues")

	// All four corpora must be present with correct states.
	f, err := Read(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if f == nil {
		t.Fatal("expected non-nil status file")
	}

	want := map[string]string{
		"code":    "completed",
		"commits": "completed",
		"context": "disabled",
		"issues":  "disabled",
	}
	for id, wantState := range want {
		cs, ok := f.Last[id]
		if !ok {
			t.Errorf("corpus %q missing from Last map", id)
			continue
		}
		if cs.State != wantState {
			t.Errorf("corpus %q: want state=%q, got %q", id, wantState, cs.State)
		}
	}
	// Check that code still has its indexed count (not clobbered).
	if f.Last["code"].IndexedCount != 100 {
		t.Errorf("code indexed_count clobbered: want 100, got %d", f.Last["code"].IndexedCount)
	}
	if f.Last["commits"].IndexedCount != 50 {
		t.Errorf("commits indexed_count clobbered: want 50, got %d", f.Last["commits"].IndexedCount)
	}
}

func TestWriter_UpdateProgress(t *testing.T) {
	dir := t.TempDir()
	projectRoot := dir
	if err := os.MkdirAll(filepath.Join(projectRoot, ".cspace"), 0o755); err != nil {
		t.Fatal(err)
	}

	w, err := NewWriter(projectRoot)
	if err != nil {
		t.Fatal(err)
	}

	w.StartCorpus("code")
	// Force a flush by resetting lastFlush.
	w.mu.Lock()
	w.lastFlush = time.Time{}
	w.mu.Unlock()
	w.UpdateProgress(10, 100)

	f, err := Read(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if f.Current == nil {
		t.Fatal("expected current to be set")
	}
	if f.Current.Progress.Done != 10 || f.Current.Progress.Total != 100 {
		t.Errorf("unexpected progress: %+v", f.Current.Progress)
	}
}
