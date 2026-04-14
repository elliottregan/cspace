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

// ListOptions filters entries for ListEntries and ReadEntries.
// Zero Kind means both kinds. Since/Until are inclusive when set.
type ListOptions struct {
	Kind  Kind
	Since *time.Time
	Until *time.Time
	Limit int // 0 = no limit
}

// EntrySummary is the list-view metadata for an entry (no body).
type EntrySummary struct {
	Kind  Kind
	Date  time.Time
	Slug  string
	Title string
	Path  string
}

// scannedEntry pairs a parsed Entry with the file path it came from.
type scannedEntry struct {
	Entry
	Path string
}

// scanEntries reads and parses every entry file under the selected kinds
// exactly once, applying Since/Until filters, sort, and Limit.
func (s *Store) scanEntries(opts ListOptions) ([]scannedEntry, error) {
	kinds := []Kind{KindDecision, KindDiscovery}
	if opts.Kind != "" {
		kinds = []Kind{opts.Kind}
	}
	var out []scannedEntry
	for _, k := range kinds {
		dir := filepath.Join(s.ContextDir(), subdirFor(k))
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, de := range entries {
			if de.IsDir() || !strings.HasSuffix(de.Name(), ".md") {
				continue
			}
			path := filepath.Join(dir, de.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			e, err := ParseEntry(string(raw))
			if err != nil {
				continue // skip malformed files silently
			}
			if opts.Since != nil && e.Date.Before(*opts.Since) {
				continue
			}
			if opts.Until != nil && e.Date.After(*opts.Until) {
				continue
			}
			e.Kind = k
			e.Slug = strings.TrimSuffix(de.Name(), ".md")
			out = append(out, scannedEntry{Entry: e, Path: path})
		}
	}
	sortScanned(out)
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

// ListEntries returns metadata for matching entries, newest first.
func (s *Store) ListEntries(opts ListOptions) ([]EntrySummary, error) {
	scanned, err := s.scanEntries(opts)
	if err != nil {
		return nil, err
	}
	out := make([]EntrySummary, 0, len(scanned))
	for _, se := range scanned {
		out = append(out, EntrySummary{
			Kind:  se.Kind,
			Date:  se.Date,
			Slug:  se.Slug,
			Title: se.Title,
			Path:  se.Path,
		})
	}
	return out, nil
}

// ReadEntries returns full entry bodies matching opts, newest first.
func (s *Store) ReadEntries(opts ListOptions) ([]Entry, error) {
	scanned, err := s.scanEntries(opts)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(scanned))
	for _, se := range scanned {
		out = append(out, se.Entry)
	}
	return out, nil
}

// ReadHuman returns the content of a human-owned file.
// section must be one of: "direction", "principles", "roadmap".
func (s *Store) ReadHuman(section string) (string, error) {
	if !isHumanSection(section) {
		return "", fmt.Errorf("unknown human section: %q", section)
	}
	path := filepath.Join(s.ContextDir(), section+".md")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// RemoveEntry deletes an entry by its full filename. The slug argument is the
// on-disk filename (with or without .md); it must match the charset produced
// by Slugify (lowercase letters, digits, and hyphens). Callers cannot pass a
// bare title-slug — the date prefix is part of the identifier.
func (s *Store) RemoveEntry(kind Kind, slug string) error {
	slug = strings.TrimSuffix(slug, ".md")
	if !isValidFilenameSlug(slug) {
		return fmt.Errorf("invalid slug: %q (must be non-empty and contain only [a-z0-9-])", slug)
	}
	path := filepath.Join(s.ContextDir(), subdirFor(kind), slug+".md")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("entry not found: %s/%s", kind, slug)
		}
		return fmt.Errorf("stat %s: %w", path, err)
	}
	return os.Remove(path)
}

// isValidFilenameSlug returns true when slug is a safe on-disk name:
// non-empty, composed only of the characters Slugify emits.
func isValidFilenameSlug(slug string) bool {
	if slug == "" {
		return false
	}
	for _, r := range slug {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return false
		}
	}
	return true
}

func subdirFor(k Kind) string {
	switch k {
	case KindDecision:
		return "decisions"
	case KindDiscovery:
		return "discoveries"
	}
	return ""
}

func isHumanSection(name string) bool {
	switch name {
	case "direction", "principles", "roadmap":
		return true
	}
	return false
}

func sortScanned(s []scannedEntry) {
	for i := 1; i < len(s); i++ {
		j := i
		for j > 0 && lessScanned(s[j], s[j-1]) {
			s[j], s[j-1] = s[j-1], s[j]
			j--
		}
	}
}

func lessScanned(a, b scannedEntry) bool {
	if !a.Date.Equal(b.Date) {
		return a.Date.After(b.Date)
	}
	return a.Slug < b.Slug
}
