package diagnostics

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Tailer watches an event log directory for new NDJSON lines and feeds
// them into the Hub. It discovers agent subdirectories, finds the latest
// session file in each, and tails from the current position.
type Tailer struct {
	hub      *Hub
	root     string // e.g. /logs/events
	interval time.Duration

	mu      sync.Mutex
	tracked map[string]*tailedFile // instance → file state
	done    chan struct{}
}

type tailedFile struct {
	path   string
	offset int64
}

// NewTailer creates a tailer that polls the event log root directory.
func NewTailer(hub *Hub, root string, interval time.Duration) *Tailer {
	if interval <= 0 {
		interval = 1 * time.Second
	}
	return &Tailer{
		hub:      hub,
		root:     root,
		interval: interval,
		tracked:  make(map[string]*tailedFile),
		done:     make(chan struct{}),
	}
}

// Run starts the polling loop. Blocks until Stop is called.
func (t *Tailer) Run() {
	// Initial scan.
	t.scan()

	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()

	for {
		select {
		case <-t.done:
			return
		case <-ticker.C:
			t.scan()
		}
	}
}

// Stop signals the tailer to exit.
func (t *Tailer) Stop() {
	close(t.done)
}

// scan discovers instance directories, finds the latest session file in
// each, and reads new lines since the last offset.
func (t *Tailer) scan() {
	entries, err := os.ReadDir(t.root)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		instance := entry.Name()
		t.tailInstance(instance)
	}
}

// tailInstance finds the latest session file for an instance and reads
// new lines from where we left off.
func (t *Tailer) tailInstance(instance string) {
	dir := filepath.Join(t.root, instance)
	sessionFile := findLatestSession(dir)
	if sessionFile == "" {
		return
	}

	t.mu.Lock()
	tf, ok := t.tracked[instance]
	if !ok || tf.path != sessionFile {
		// New file or file changed — start from beginning for new files,
		// or reset if the session file rotated.
		tf = &tailedFile{path: sessionFile, offset: 0}
		t.tracked[instance] = tf
	}
	t.mu.Unlock()

	t.readNewLines(tf)
}

// readNewLines reads from the current offset to EOF, parsing each line
// as an Envelope and feeding it to the hub.
func (t *Tailer) readNewLines(tf *tailedFile) {
	f, err := os.Open(tf.path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Seek(tf.offset, io.SeekStart); err != nil {
		return
	}

	scanner := bufio.NewScanner(f)
	// Allow large lines (SDK messages can be big).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var env Envelope
		if err := json.Unmarshal(line, &env); err != nil {
			continue
		}
		t.hub.IngestEvent(env)
	}

	// Update offset to current position.
	pos, err := f.Seek(0, io.SeekCurrent)
	if err == nil {
		t.mu.Lock()
		tf.offset = pos
		t.mu.Unlock()
	}
}

// findLatestSession returns the path of the newest session-*.ndjson file
// in dir, by modification time. Returns "" if none found.
func findLatestSession(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	var best string
	var bestTime time.Time

	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "session-") || !strings.HasSuffix(name, ".ndjson") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(bestTime) {
			bestTime = info.ModTime()
			best = filepath.Join(dir, name)
		}
	}
	return best
}

// LoadExisting reads all existing event log files to populate initial state.
// Call before Run() to backfill the hub with historical data.
func (t *Tailer) LoadExisting() {
	entries, err := os.ReadDir(t.root)
	if err != nil {
		log.Printf("[diagnostics] load existing: %v", err)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		instance := entry.Name()
		dir := filepath.Join(t.root, instance)
		sessionFile := findLatestSession(dir)
		if sessionFile == "" {
			continue
		}

		t.mu.Lock()
		t.tracked[instance] = &tailedFile{path: sessionFile, offset: 0}
		t.mu.Unlock()

		t.readNewLines(t.tracked[instance])
	}
}
