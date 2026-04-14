package contextstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Store is the filesystem-backed context store. Root is the repo root;
// files live under Root/docs/context/. Now returns "today" for entry dating
// (overridable for tests).
type Store struct {
	Root string
	Now  func() time.Time
}

// ContextDir returns the absolute docs/context path.
func (s *Store) ContextDir() string {
	return filepath.Join(s.Root, "docs", "context")
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

// LogDecisionInput is the input to LogDecision.
type LogDecisionInput struct {
	Title        string
	Context      string
	Alternatives string
	Decision     string
	Consequences string
}

// LogDiscoveryInput is the input to LogDiscovery.
type LogDiscoveryInput struct {
	Title   string
	Finding string
	Impact  string
}

// LogDecision creates a new decision file and returns its absolute path.
func (s *Store) LogDecision(in LogDecisionInput) (string, error) {
	if strings.TrimSpace(in.Title) == "" {
		return "", fmt.Errorf("title is required")
	}
	if err := s.ensureSeeded(); err != nil {
		return "", err
	}
	e := Entry{
		Kind:  KindDecision,
		Title: in.Title,
		Date:  s.now(),
		Sections: map[string]string{
			"Context":      in.Context,
			"Alternatives": in.Alternatives,
			"Decision":     in.Decision,
			"Consequences": in.Consequences,
		},
	}
	return s.writeEntry(e, "decisions")
}

// LogDiscovery creates a new discovery file and returns its absolute path.
func (s *Store) LogDiscovery(in LogDiscoveryInput) (string, error) {
	if strings.TrimSpace(in.Title) == "" {
		return "", fmt.Errorf("title is required")
	}
	if err := s.ensureSeeded(); err != nil {
		return "", err
	}
	e := Entry{
		Kind:  KindDiscovery,
		Title: in.Title,
		Date:  s.now(),
		Sections: map[string]string{
			"Finding": in.Finding,
			"Impact":  in.Impact,
		},
	}
	return s.writeEntry(e, "discoveries")
}

func (s *Store) writeEntry(e Entry, subdir string) (string, error) {
	dir := filepath.Join(s.ContextDir(), subdir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	slug := Slugify(e.Title)
	if slug == "" {
		return "", fmt.Errorf("title produces empty slug: %q", e.Title)
	}
	base := e.Date.Format("2006-01-02") + "-" + slug

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	taken := map[string]bool{}
	for _, de := range entries {
		taken[de.Name()] = true
	}
	resolved := ResolveCollision(base, taken)

	path := filepath.Join(dir, resolved+".md")
	e.Slug = resolved
	if err := os.WriteFile(path, []byte(e.Render()), 0644); err != nil {
		return "", err
	}
	return path, nil
}

// humanSeeds are initial templates for human-owned files. Only written if missing.
var humanSeeds = map[string]string{
	"direction.md": `# Direction

<!--
Human-owned. What are we building, why, and for whom?
Keep this short and load-bearing. Agents read this on every task.
-->
`,
	"principles.md": `# Principles

<!--
Human-owned. Non-negotiable constraints and values that shape every decision.
Examples: "no vendor lock-in", "local-first", "zero-config for the default path".
-->
`,
	"roadmap.md": `# Roadmap

<!--
Human-owned. What's coming next, roughly in order.
Agents use this to understand where the current task fits.
-->
`,
}

// ensureSeeded creates docs/context/ and seeds human-owned files if missing.
// Existing files are never overwritten.
func (s *Store) ensureSeeded() error {
	if err := os.MkdirAll(s.ContextDir(), 0755); err != nil {
		return err
	}
	for name, body := range humanSeeds {
		path := filepath.Join(s.ContextDir(), name)
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			return err
		}
	}
	return nil
}
