// Package corpus defines the abstraction over indexable content types.
// A Corpus enumerates Records; the indexer embeds them and writes them
// to Qdrant. The same pipeline serves commits, code, and future corpora.
package corpus

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"path/filepath"
)

// Record is one unit of content to be indexed. A Record may represent a whole
// file, a chunk of a file, or a commit; the Kind field disambiguates.
type Record struct {
	// Path is the primary identifier: relative file path (code), commit hash
	// (commits), or similar.
	Path string

	// LineStart and LineEnd are 1-based inclusive line ranges for chunked
	// code records. Zero for whole-file or commit records.
	LineStart int
	LineEnd   int

	// Kind is "file", "chunk", or "commit".
	Kind string

	// ContentHash is the hex sha256 of the source bytes, used for
	// change detection.
	ContentHash string

	// Extra carries corpus-specific metadata that should land in the Qdrant
	// payload (e.g., commit Subject, Date).
	Extra map[string]any

	// EmbedText is the text to send to the embedding model. Populated by the
	// corpus at enumeration time so the indexer does not need to know the
	// corpus-specific embedding format.
	EmbedText string
}

// ID returns a deterministic 64-bit Qdrant point ID for this Record.
func (r Record) ID() uint64 {
	h := sha256.New()
	// sha256.Hash.Write never returns an error, but errcheck doesn't know that.
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00%d\x00%d", r.Kind, r.Path, r.LineStart, r.LineEnd)
	sum := h.Sum(nil)
	return binary.BigEndian.Uint64(sum[:8])
}

// ProjectHash returns a stable 8-char hex hash for a project root path,
// used as a suffix in Qdrant collection names.
func ProjectHash(repoPath string) string {
	abs, _ := filepath.Abs(repoPath)
	h := sha256.Sum256([]byte(abs))
	return fmt.Sprintf("%x", h[:4])
}

// Corpus is the interface an indexable content type implements.
type Corpus interface {
	// ID is the stable corpus identifier, e.g. "commits", "code".
	ID() string

	// Collection returns the Qdrant collection name for this corpus + project.
	Collection(projectRoot string) string

	// Enumerate emits Records, one per unit of content. The channel is closed
	// when enumeration completes. Errors are reported via the errs channel
	// but do not halt enumeration of unaffected records.
	Enumerate(projectRoot string) (<-chan Record, <-chan error)
}
