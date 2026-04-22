// Package status tracks the state of search index runs so agents and CLI
// users can tell whether the index is idle, running, complete, or failed
// without inspecting lock files or log tails.
//
// State is persisted to .cspace/search-index-status.json via atomic
// write-tmp-then-rename so concurrent readers never see a partial file.
package status

import (
	"encoding/json"
	"fmt"
	"io"
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

// ComputedStatus is the unified output shape for the status command — both
// the MCP tool and the CLI serialize this same struct so their JSON output
// is guaranteed identical. Extracted per PR #61 review item #5.
type ComputedStatus struct {
	Corpora map[string]ComputedCorpus `json:"corpora"`
	Current *RunningState             `json:"current"`
}

// ComputedCorpus describes one corpus's index state plus staleness.
type ComputedCorpus struct {
	State        string `json:"state"` // completed, failed, disabled, unknown
	FinishedAt   string `json:"finished_at,omitempty"`
	DurationMS   int64  `json:"duration_ms,omitempty"`
	IndexedCount int    `json:"indexed_count,omitempty"`
	Error        string `json:"error,omitempty"`
	Stale        bool   `json:"stale,omitempty"`
	StaleReason  string `json:"stale_reason,omitempty"`
}

// StalenessChecker is a callback used by Compute to check staleness for
// code/commits corpora. It decouples the status package from qdrant/corpus.
type StalenessChecker func(corpusID string) (stale bool, reason string)

// Compute builds a ComputedStatus from the on-disk status file and config.
// disabledCorpora is the set of corpus IDs that are disabled in config.
// checkStaleness is an optional callback for staleness checks (may be nil).
func Compute(projectRoot string, disabledCorpora map[string]bool, checkStaleness StalenessChecker) (*ComputedStatus, error) {
	sf, err := Read(projectRoot)
	if err != nil {
		return nil, err
	}

	allCorpora := []string{"code", "commits", "context", "issues"}
	out := &ComputedStatus{Corpora: make(map[string]ComputedCorpus)}
	if sf != nil {
		out.Current = sf.Current
	}

	for _, id := range allCorpora {
		co := ComputedCorpus{State: "unknown"}

		if disabledCorpora[id] {
			co.State = "disabled"
			out.Corpora[id] = co
			continue
		}

		if sf != nil {
			if cs, ok := sf.Last[id]; ok {
				co.State = cs.State
				if !cs.FinishedAt.IsZero() {
					co.FinishedAt = cs.FinishedAt.Format(time.RFC3339)
				}
				co.DurationMS = cs.DurationMS
				co.IndexedCount = cs.IndexedCount
				co.Error = cs.Error
			}
		}

		if co.State == "completed" && (id == "code" || id == "commits") && checkStaleness != nil {
			if stale, reason := checkStaleness(id); stale {
				co.Stale = true
				co.StaleReason = reason
			}
		}

		out.Corpora[id] = co
	}

	return out, nil
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

	// ErrLog receives flush errors (disk full, permission denied, etc.)
	// instead of silently dropping them. Defaults to os.Stderr when nil.
	ErrLog io.Writer
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
	now := time.Now().UTC()
	w.file.Current = &RunningState{
		Corpus:    corpusID,
		StartedAt: now,
	}
	w.file.UpdatedAt = now
	w.flush()
}

// UpdateProgress records incremental progress. Throttled to ~1/sec.
func (w *Writer) UpdateProgress(done, total int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file.Current == nil {
		return
	}
	now := time.Now().UTC()
	w.file.Current.Progress = Progress{Done: done, Total: total}
	w.file.UpdatedAt = now
	if time.Since(w.lastFlush) >= time.Second {
		w.flush()
	}
}

// FinishCorpus records a successful completion.
func (w *Writer) FinishCorpus(corpusID string, startedAt time.Time, indexedCount int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now().UTC()
	w.file.Last[corpusID] = CorpusState{
		State:        "completed",
		FinishedAt:   now,
		DurationMS:   time.Since(startedAt).Milliseconds(),
		IndexedCount: indexedCount,
	}
	w.file.Current = nil
	w.file.UpdatedAt = now
	w.flush()
}

// FailCorpus records a failed run.
func (w *Writer) FailCorpus(corpusID string, startedAt time.Time, runErr error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now().UTC()
	w.file.Last[corpusID] = CorpusState{
		State:      "failed",
		FinishedAt: now,
		DurationMS: time.Since(startedAt).Milliseconds(),
		Error:      runErr.Error(),
	}
	w.file.Current = nil
	w.file.UpdatedAt = now
	w.flush()
}

// DisableCorpus records that a corpus is intentionally disabled. Short-circuits
// without flushing if the corpus is already disabled.
func (w *Writer) DisableCorpus(corpusID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// No-op if already disabled — avoids unnecessary I/O in init loops.
	if cs, ok := w.file.Last[corpusID]; ok && cs.State == "disabled" {
		return
	}
	now := time.Now().UTC()
	w.file.Last[corpusID] = CorpusState{State: "disabled"}
	w.file.UpdatedAt = now
	w.flush()
}

// flush writes the status file atomically (write tmp + rename). Caller
// must hold w.mu. Errors are reported to w.ErrLog (defaults to os.Stderr).
func (w *Writer) flush() {
	errLog := w.ErrLog
	if errLog == nil {
		errLog = os.Stderr
	}
	data, err := json.MarshalIndent(&w.file, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(errLog, "status: marshal error: %v\n", err)
		return
	}
	tmp := w.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		_, _ = fmt.Fprintf(errLog, "status: write error: %v\n", err)
		return
	}
	if err := os.Rename(tmp, w.path); err != nil {
		_, _ = fmt.Fprintf(errLog, "status: rename error: %v\n", err)
		return
	}
	w.lastFlush = time.Now()
}
