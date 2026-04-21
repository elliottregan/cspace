package corpus

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CodeCorpus indexes git-tracked source files.
type CodeCorpus struct {
	Filter Filter
	Chunk  ChunkConfig
}

// ID returns the corpus identifier.
func (c *CodeCorpus) ID() string { return "code" }

// Collection returns the Qdrant collection name for this corpus in the
// given project.
func (c *CodeCorpus) Collection(projectRoot string) string {
	return "code-" + ProjectHash(projectRoot)
}

// Enumerate walks git ls-files, filters, chunks oversized files, and emits
// one Record per chunk (single-chunk files get one record with Kind=file).
func (c *CodeCorpus) Enumerate(projectRoot string) (<-chan Record, <-chan error) {
	out := make(chan Record)
	errs := make(chan error, 8)
	go func() {
		defer close(out)
		defer close(errs)

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "git", "-C", projectRoot, "ls-files")
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			errs <- fmt.Errorf("git ls-files pipe: %w", err)
			return
		}
		if err := cmd.Start(); err != nil {
			errs <- fmt.Errorf("git ls-files start: %w", err)
			return
		}

		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			rel := sc.Text()
			abs := filepath.Join(projectRoot, rel)
			if !c.Filter.Accept(abs) {
				continue
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				errs <- fmt.Errorf("read %s: %w", rel, err)
				continue
			}
			hash := fmt.Sprintf("%x", sha256.Sum256(data))
			chunks := Chunk(data, c.Chunk)
			kind := "file"
			if len(chunks) > 1 {
				kind = "chunk"
			}
			for _, ch := range chunks {
				rec := Record{
					Path:        rel,
					Kind:        kind,
					LineStart:   ch.LineStart,
					LineEnd:     ch.LineEnd,
					ContentHash: hash,
					EmbedText:   formatCodeEmbedText(rel, ch.Text),
					Extra: map[string]any{
						"mtime": time.Now().UTC().Format(time.RFC3339),
					},
				}
				out <- rec
			}
		}
		if err := sc.Err(); err != nil {
			errs <- fmt.Errorf("scan git ls-files: %w", err)
		}
		_ = cmd.Wait()
	}()
	return out, errs
}

// formatCodeEmbedText prepends a small header so the embedder has the
// path context. Jina v5 benefits from this signal.
func formatCodeEmbedText(path, body string) string {
	const max = 12000
	header := "File: " + path + "\n\n"
	if len(body)+len(header) > max {
		body = body[:max-len(header)]
	}
	return header + strings.TrimRight(body, "\x00")
}
