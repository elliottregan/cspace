package control

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"
)

// EventLine is one parsed events.ndjson record, narrowed to what the
// dashboard renders: the detail band shows the timestamps and types, and the
// supervisor view shows the assistant's own words with a one-line summary of
// each tool it called.
type EventLine struct {
	Ts      string
	Kind    string
	Type    string
	Subtype string
	// Text is the assistant's text blocks, joined — or, for the SDK shapes
	// whose message content is a bare string, that string.
	Text string
	// Tools is one short line per tool call, "<name>(<argument>)".
	Tools []string
}

// eventRecord mirrors the on-disk NDJSON line shape. Content is raw because
// the Agent SDK sends it both as a block array and as a plain string, and a
// typed field would fail the whole line on the shape it did not expect —
// which for a tail means silently dropping events.
type eventRecord struct {
	Ts   string `json:"ts"`
	Kind string `json:"kind"`
	Data struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"data"`
}

// contentBlock is one element of the block-array form.
type contentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// decodeContent fills in an EventLine's Text and Tools from whichever shape
// the message content arrived in.
func decodeContent(raw json.RawMessage, line *EventLine) {
	if len(raw) == 0 {
		return
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			line.Text = s
		}
		return
	}
	var texts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				texts = append(texts, b.Text)
			}
		case "tool_use":
			line.Tools = append(line.Tools, toolSummary(b.Name, b.Input))
		}
	}
	line.Text = strings.Join(texts, "\n\n")
}

// toolArgKeys are the input fields worth showing, most specific first. A
// tool call's whole input is often a file's contents; what a person reading
// a feed wants is which file.
var toolArgKeys = []string{"file_path", "command", "path", "pattern", "url", "description", "prompt", "query"}

// toolSummaryLimit is how much of the argument survives, in runes.
const toolSummaryLimit = 60

// toolSummary renders one tool call as a single line.
func toolSummary(name string, input json.RawMessage) string {
	if name == "" {
		name = "tool"
	}
	var args map[string]any
	if json.Unmarshal(input, &args) != nil || len(args) == 0 {
		return name
	}
	pick := ""
	for _, k := range toolArgKeys {
		if v, ok := args[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				pick = s
				break
			}
		}
	}
	if pick == "" {
		keys := make([]string, 0, len(args))
		for k := range args {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if s, ok := args[keys[0]].(string); ok {
			pick = s
		}
	}
	pick = strings.ReplaceAll(strings.TrimSpace(pick), "\n", " ")
	if r := []rune(pick); len(r) > toolSummaryLimit {
		pick = string(r[:toolSummaryLimit]) + "…"
	}
	if pick == "" {
		return name
	}
	return name + "(" + pick + ")"
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
		line := EventLine{
			Ts:      rec.Ts,
			Kind:    rec.Kind,
			Type:    rec.Data.Type,
			Subtype: rec.Data.Subtype,
		}
		decodeContent(rec.Data.Message.Content, &line)
		all = append(all, line)
	}
	// A Scanner error (other than a too-long final token) is unusual; ignore it
	// so a truncated tail still renders what parsed.
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

// Events returns the tail of a sandbox's supervisor event log, newest last.
func (c *Client) Events(project, sandbox string, n int) ([]EventLine, error) {
	if c.home == "" {
		return nil, ErrNoHome
	}
	return TailEvents(SessionEventsPath(c.home, project, sandbox), n)
}
