package corpus

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ContextCorpus indexes the layered planning artifacts under .cspace/context/:
// direction.md, principles.md, roadmap.md, and the findings/, decisions/,
// discoveries/ subdirectories.
type ContextCorpus struct{}

// ID returns the stable corpus identifier.
func (c *ContextCorpus) ID() string { return "context" }

// Collection returns the Qdrant collection name for this corpus + project.
func (c *ContextCorpus) Collection(projectRoot string) string {
	return "context-" + ProjectHash(projectRoot)
}

// Enumerate emits one Record per context artifact. Missing subdirectories are
// silently skipped. Files are not chunked — just truncated to ~12000 chars.
func (c *ContextCorpus) Enumerate(projectRoot string) (<-chan Record, <-chan error) {
	out := make(chan Record)
	errs := make(chan error, 8)
	go func() {
		defer close(out)
		defer close(errs)

		ctxDir := filepath.Join(projectRoot, ".cspace", "context")

		// Top-level context files: each is one record with kind="context".
		for _, name := range []string{"direction.md", "principles.md", "roadmap.md"} {
			abs := filepath.Join(ctxDir, name)
			data, err := os.ReadFile(abs)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				errs <- fmt.Errorf("read %s: %w", name, err)
				continue
			}
			subkind := strings.TrimSuffix(name, ".md")
			rel := filepath.ToSlash(filepath.Join(".cspace", "context", name))
			out <- contextRecord(rel, subkind, "context", data)
		}

		// Subdirectories: findings, decisions, discoveries.
		subdirs := []struct {
			dir  string
			kind string
		}{
			{"findings", "finding"},
			{"decisions", "decision"},
			{"discoveries", "discovery"},
		}
		for _, sub := range subdirs {
			subDir := filepath.Join(ctxDir, sub.dir)
			entries, err := os.ReadDir(subDir)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				errs <- fmt.Errorf("readdir %s: %w", sub.dir, err)
				continue
			}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
					continue
				}
				abs := filepath.Join(subDir, entry.Name())
				data, err := os.ReadFile(abs)
				if err != nil {
					errs <- fmt.Errorf("read %s/%s: %w", sub.dir, entry.Name(), err)
					continue
				}
				rel := filepath.ToSlash(filepath.Join(".cspace", "context", sub.dir, entry.Name()))
				out <- contextRecord(rel, sub.kind, sub.kind, data)
			}
		}
	}()
	return out, errs
}

// contextRecord builds a Record for a context artifact.
func contextRecord(relPath, labelForHeader, kind string, data []byte) Record {
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	extra := map[string]any{}
	if kind == "context" {
		// Top-level files carry a subkind for disambiguation.
		extra["subkind"] = labelForHeader
	}
	return Record{
		Path:        relPath,
		Kind:        kind,
		ContentHash: hash,
		EmbedText:   formatContextEmbedText(labelForHeader, relPath, string(data)),
		Extra:       extra,
	}
}

// formatContextEmbedText prepends a small header and truncates to ~12000 chars.
func formatContextEmbedText(label, path, body string) string {
	const max = 12000
	header := "Context (" + label + "): " + path + "\n\n"
	if len(body)+len(header) > max {
		body = body[:max-len(header)]
	}
	return header + body
}
