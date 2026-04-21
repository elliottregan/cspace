package corpus

import "strings"

// ChunkConfig tunes the chunker.
type ChunkConfig struct {
	Max     int // max chars per chunk
	Overlap int // chars of overlap between consecutive chunks
}

// ChunkOut is one contiguous text slice with a 1-based inclusive line range.
type ChunkOut struct {
	Text      string
	LineStart int
	LineEnd   int
}

// Chunk splits content into ChunkOut slices respecting max size and overlap.
// Line numbers are 1-based inclusive.
func Chunk(content []byte, cfg ChunkConfig) []ChunkOut {
	s := string(content)
	if cfg.Max <= 0 {
		cfg.Max = 12000
	}
	if len(s) <= cfg.Max {
		return []ChunkOut{{Text: s, LineStart: 1, LineEnd: lineCount(s)}}
	}
	var out []ChunkOut
	start := 0
	for start < len(s) {
		end := start + cfg.Max
		if end >= len(s) {
			end = len(s)
		} else {
			// Prefer to cut at a newline within the last 500 chars of the max
			// window to avoid mid-line splits.
			window := s[start:end]
			if nl := strings.LastIndex(window, "\n"); nl > cfg.Max-500 && nl > 0 {
				end = start + nl + 1
			}
		}
		text := s[start:end]
		ls := lineOf(s, start)
		le := lineOf(s, end-1)
		out = append(out, ChunkOut{Text: text, LineStart: ls, LineEnd: le})
		if end >= len(s) {
			break
		}
		next := end - cfg.Overlap
		if next <= start {
			next = start + 1
		}
		start = next
	}
	return out
}

func lineCount(s string) int {
	if len(s) == 0 {
		return 1
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	if n == 0 {
		n = 1
	}
	return n
}

func lineOf(s string, off int) int {
	if len(s) == 0 {
		return 1
	}
	if off >= len(s) {
		off = len(s) - 1
	}
	if off < 0 {
		return 1
	}
	return 1 + strings.Count(s[:off+1], "\n")
}
