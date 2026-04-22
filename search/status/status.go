// Package status tracks the state of search index runs so agents and CLI
// users can tell whether the index is idle, running, complete, or failed
// without inspecting lock files or log tails.
//
// State is persisted to .cspace/search-index-status.json via atomic
// write-tmp-then-rename so concurrent readers never see a partial file.
package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// File is the on-disk JSON shape written to .cspace/search-index-status.json.
type File struct {
	UpdatedAt time.Time              `json:"updated_at"`
	Current   *RunningState          `json:"current"` // nil when idle
	Last      map[string]CorpusState `json:"last"`    // keyed by corpus id
}

// RunningState describes an in-progress index run.
type RunningState struct {
	Corpus    string    `json:"corpus"`
	StartedAt time.Time `json:"started_at"`
	Progress  Progress  `json:"progress"`
}

// Progress tracks items processed vs total.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// CorpusState records the outcome of the most recent index run for one corpus.
type CorpusState struct {
	State        string    `json:"state"` // "completed", "failed", "disabled"
	FinishedAt   time.Time `json:"finished_at,omitempty"`
	DurationMS   int64     `json:"duration_ms,omitempty"`
	IndexedCount int       `json:"indexed_count,omitempty"`
	Error        string    `json:"error,omitempty"`
}

// StatusPath returns the canonical path for the status file inside a project.
func StatusPath(projectRoot string) string {
	return filepath.Join(projectRoot, ".cspace", "search-index-status.json")
}

// Read loads and parses the status file. Returns nil (not an error) when the
// file does not exist — the caller can treat that as "never indexed".
func Read(projectRoot string) (*File, error) {
	data, err := os.ReadFile(StatusPath(projectRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// Writer manages atomic writes to the status file. It is safe for concurrent
// use from a single process (the indexer's progress callback fires from
// goroutines). Writes are throttled to at most once per second to avoid
// hammering the filesystem during fast progress updates.
type Writer struct {
	path string
	mu   sync.Mutex
	file File

	// lastFlush tracks the wall-clock time of the most recent disk write so
	// we can throttle progress-driven updates.
	lastFlush time.Time
}

// NewWriter creates a Writer targeting projectRoot. It loads any existing
// status file so incremental updates preserve prior corpus states.
func NewWriter(projectRoot string) (*Writer, error) {
	p := StatusPath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	w := &Writer{path: p}
	existing, err := Read(projectRoot)
	if err == nil && existing != nil {
		w.file = *existing
	}
	if w.file.Last == nil {
		w.file.Last = make(map[string]CorpusState)
	}
	return w, nil
}

// StartCorpus records that an index run has begun for corpusID. Always
// flushes immediately so a fast query sees "indexing is running".
func (w *Writer) StartCorpus(corpusID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.file.Current = &RunningState{
		Corpus:    corpusID,
		StartedAt: time.Now().UTC(),
	}
	w.file.UpdatedAt = time.Now().UTC()
	w.flush()
}

// UpdateProgress records incremental progress. Throttled to ~1/sec.
func (w *Writer) UpdateProgress(done, total int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file.Current == nil {
		return
	}
	w.file.Current.Progress = Progress{Done: done, Total: total}
	w.file.UpdatedAt = time.Now().UTC()
	if time.Since(w.lastFlush) >= time.Second {
		w.flush()
	}
}

// FinishCorpus records a successful completion.
func (w *Writer) FinishCorpus(corpusID string, startedAt time.Time, indexedCount int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.file.Last[corpusID] = CorpusState{
		State:        "completed",
		FinishedAt:   time.Now().UTC(),
		DurationMS:   time.Since(startedAt).Milliseconds(),
		IndexedCount: indexedCount,
	}
	w.file.Current = nil
	w.file.UpdatedAt = time.Now().UTC()
	w.flush()
}

// FailCorpus records a failed run.
func (w *Writer) FailCorpus(corpusID string, startedAt time.Time, runErr error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.file.Last[corpusID] = CorpusState{
		State:      "failed",
		FinishedAt: time.Now().UTC(),
		DurationMS: time.Since(startedAt).Milliseconds(),
		Error:      runErr.Error(),
	}
	w.file.Current = nil
	w.file.UpdatedAt = time.Now().UTC()
	w.flush()
}

// DisableCorpus records that a corpus is intentionally disabled.
func (w *Writer) DisableCorpus(corpusID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.file.Last[corpusID] = CorpusState{State: "disabled"}
	w.file.UpdatedAt = time.Now().UTC()
	w.flush()
}

// flush writes the status file atomically (write tmp + rename). Caller
// must hold w.mu.
func (w *Writer) flush() {
	data, err := json.MarshalIndent(&w.file, "", "  ")
	if err != nil {
		return
	}
	tmp := w.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, w.path)
	w.lastFlush = time.Now()
}
