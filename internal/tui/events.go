package tui

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

// EventLine is one parsed events.ndjson record, narrowed to what the detail
// pane renders.
type EventLine struct {
	Ts      string
	Kind    string
	Type    string
	Subtype string
}

// eventRecord mirrors the on-disk NDJSON line shape.
type eventRecord struct {
	Ts   string `json:"ts"`
	Kind string `json:"kind"`
	Data struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
	} `json:"data"`
}

// SessionEventsPath is the host path of a sandbox's supervisor event log. The
// "primary" segment is the supervisor's hardcoded SESSION_ID — do not make it
// configurable. Mirrors the literal join cmd_up.go uses for the /sessions mount.
func SessionEventsPath(home, project, sandbox string) string {
	return filepath.Join(home, ".cspace", "sessions", project, sandbox, "primary", "events.ndjson")
}

// TailEvents returns the last n parsed lines of the events.ndjson at path.
// A missing file yields (nil, nil) — pre-first-event or wiped-by-down is not an
// error. Malformed lines (including a partially-flushed trailing line) are
// skipped, not fatal. It reads the whole current-generation file then keeps the
// last n valid lines; the file single-generation-rotates at 10 MiB, so it is
// bounded. This stateless full re-read is rotation-SAFE (it always reads the
// current generation from scratch); it does not read events.ndjson.1, so right
// after a rotation the tail may briefly hold fewer than n lines.
func TailEvents(path string, n int) ([]EventLine, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var all []EventLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var rec eventRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue // tolerate malformed / partial lines
		}
		all = append(all, EventLine{
			Ts:      rec.Ts,
			Kind:    rec.Kind,
			Type:    rec.Data.Type,
			Subtype: rec.Data.Subtype,
		})
	}
	// A Scanner error (other than a too-long final token) is unusual; ignore it
	// so a truncated tail still renders what parsed.
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}
