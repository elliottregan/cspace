package status

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// TestWriter_FlushReportsErrors verifies that flush errors are written to
// the ErrLog writer rather than silently dropped. PR #61 review item #6.
func TestWriter_FlushReportsErrors(t *testing.T) {
	// Point the Writer at a nonexistent directory — writes will fail.
	var errBuf bytes.Buffer
	w := &Writer{
		path:   "/nonexistent-dir/status.json",
		ErrLog: &errBuf,
		file:   File{Last: make(map[string]CorpusState)},
	}

	w.FinishCorpus("code", time.Now(), 42)

	if errBuf.Len() == 0 {
		t.Error("expected flush error to be written to ErrLog, got nothing")
	}
	if !strings.Contains(errBuf.String(), "status:") {
		t.Errorf("expected 'status:' prefix in error, got: %s", errBuf.String())
	}
}

// TestWriter_DisableCorpusShortCircuit verifies that calling DisableCorpus
// when the corpus is already disabled doesn't flush (no I/O). PR #61 item #9.
func TestWriter_DisableCorpusShortCircuit(t *testing.T) {
	dir := t.TempDir()
	projectRoot := dir
	if err := os.MkdirAll(filepath.Join(projectRoot, ".cspace"), 0o755); err != nil {
		t.Fatal(err)
	}

	w, err := NewWriter(projectRoot)
	if err != nil {
		t.Fatal(err)
	}

	// First call: should flush.
	w.DisableCorpus("context")
	stat1, err := os.Stat(StatusPath(projectRoot))
	if err != nil {
		t.Fatal(err)
	}
	modTime1 := stat1.ModTime()

	// Let a bit of time pass so mod times differ.
	time.Sleep(10 * time.Millisecond)

	// Second call: should short-circuit (no flush).
	w.DisableCorpus("context")
	stat2, err := os.Stat(StatusPath(projectRoot))
	if err != nil {
		t.Fatal(err)
	}
	modTime2 := stat2.ModTime()

	if !modTime1.Equal(modTime2) {
		t.Error("expected DisableCorpus to short-circuit when already disabled — mod time changed")
	}
}

// TestCompute_Basic verifies the shared Compute function. PR #61 review item #5.
func TestCompute_Basic(t *testing.T) {
	dir := t.TempDir()
	projectRoot := dir
	if err := os.MkdirAll(filepath.Join(projectRoot, ".cspace"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a status file with code=completed, commits=failed.
	w, err := NewWriter(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	w.FinishCorpus("code", time.Now(), 100)
	w2, _ := NewWriter(projectRoot)
	w2.FailCorpus("commits", time.Now(), errors.New("embed: timeout"))

	disabled := map[string]bool{"context": true, "issues": true}
	cs, err := Compute(projectRoot, true, disabled, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !cs.Enabled {
		t.Errorf("expected Enabled=true in output")
	}
	if cs.Corpora["code"].State != "completed" {
		t.Errorf("expected code=completed, got %q", cs.Corpora["code"].State)
	}
	if cs.Corpora["commits"].State != "failed" {
		t.Errorf("expected commits=failed, got %q", cs.Corpora["commits"].State)
	}
	if cs.Corpora["context"].State != "disabled" {
		t.Errorf("expected context=disabled, got %q", cs.Corpora["context"].State)
	}
	if cs.Corpora["issues"].State != "disabled" {
		t.Errorf("expected issues=disabled, got %q", cs.Corpora["issues"].State)
	}
	if cs.Corpora["code"].IndexedCount != 100 {
		t.Errorf("expected code indexed_count=100, got %d", cs.Corpora["code"].IndexedCount)
	}
}

// TestCompute_SearchDisabled verifies that when the master switch is
// off, Compute returns Enabled=false and an empty corpora map — no
// leaking prior state from a previous session when enabled was true.
func TestCompute_SearchDisabled(t *testing.T) {
	dir := t.TempDir()
	projectRoot := dir
	if err := os.MkdirAll(filepath.Join(projectRoot, ".cspace"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a status file so we can verify it is NOT surfaced.
	w, _ := NewWriter(projectRoot)
	w.FinishCorpus("code", time.Now(), 100)

	cs, err := Compute(projectRoot, false, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cs.Enabled {
		t.Errorf("expected Enabled=false when master switch is off")
	}
	if len(cs.Corpora) != 0 {
		t.Errorf("expected empty corpora map when disabled, got %d entries", len(cs.Corpora))
	}
	if cs.Current != nil {
		t.Errorf("expected Current=nil when disabled")
	}
}
