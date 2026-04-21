package corpus

import (
	"strings"
	"testing"
)

func TestChunk_SmallFile_OneChunkWholeFile(t *testing.T) {
	content := "line1\nline2\nline3\n"
	chunks := Chunk([]byte(content), ChunkConfig{Max: 12000, Overlap: 0})
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].LineStart != 1 || chunks[0].LineEnd != 3 {
		t.Errorf("expected lines 1-3, got %d-%d", chunks[0].LineStart, chunks[0].LineEnd)
	}
	if chunks[0].Text != content {
		t.Errorf("text differs from input")
	}
}

func TestChunk_EmptyFile_OneEmptyChunk(t *testing.T) {
	chunks := Chunk([]byte(""), ChunkConfig{Max: 100, Overlap: 0})
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].LineStart != 1 || chunks[0].LineEnd != 1 {
		t.Errorf("expected lines 1-1 for empty, got %d-%d", chunks[0].LineStart, chunks[0].LineEnd)
	}
}

func TestChunk_LargeFile_SplitsWithOverlap(t *testing.T) {
	// 200 lines of 100 chars each = ~20k chars. Chunk at 8000 → multiple chunks.
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString(strings.Repeat("x", 99))
		b.WriteString("\n")
	}
	content := b.String()
	chunks := Chunk([]byte(content), ChunkConfig{Max: 8000, Overlap: 200})
	if len(chunks) < 2 {
		t.Fatalf("expected ≥2 chunks, got %d", len(chunks))
	}
	if chunks[0].LineStart != 1 {
		t.Errorf("first chunk should start at line 1, got %d", chunks[0].LineStart)
	}
	// Consecutive chunks must overlap in line ranges.
	for i := 1; i < len(chunks); i++ {
		if chunks[i].LineStart > chunks[i-1].LineEnd {
			t.Errorf("chunk %d starts at line %d after previous ended at line %d — no overlap",
				i, chunks[i].LineStart, chunks[i-1].LineEnd)
		}
	}
	// Last chunk's LineEnd should reach the last line.
	lastLine := 200
	if chunks[len(chunks)-1].LineEnd < lastLine {
		t.Errorf("last chunk ends at line %d, expected ≥%d", chunks[len(chunks)-1].LineEnd, lastLine)
	}
}

func TestChunk_NoTrailingNewline(t *testing.T) {
	content := "one\ntwo\nthree"
	chunks := Chunk([]byte(content), ChunkConfig{Max: 100, Overlap: 0})
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].LineEnd != 3 {
		t.Errorf("expected LineEnd=3, got %d", chunks[0].LineEnd)
	}
}
