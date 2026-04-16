package contextstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Store is the filesystem-backed context store. Root is the repo root;
// files live under Root/.cspace/context/. Now returns "today" for entry dating
// (overridable for tests).
type Store struct {
	Root string
	Now  func() time.Time
}

// ContextDir returns the absolute .cspace/context path.
func (s *Store) ContextDir() string {
	return filepath.Join(s.Root, ".cspace", "context")
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

// LogFindingInput is the input to LogFinding.
type LogFindingInput struct {
	Title    string
	Category string // one of ValidFindingCategories
	Summary  string
	Details  string
	Status   string   // optional; defaults to "open"
	Tags     []string // optional
	Related  []string // optional — cross-reference slugs or URLs
	Author   string   // optional — recorded in the first Updates line
}

// AppendFindingInput is the input to AppendToFinding.
type AppendFindingInput struct {
	Slug   string // required — findings/<date>-<slug>.md (without .md)
	Note   string // body of the new ### update subheading
	Status string // optional; transitions the finding's status when set
	Author string // optional; recorded in the update's byline
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

// LogFinding creates a new finding file and returns its absolute path.
// Category is required and must be one of ValidFindingCategories. Status
// defaults to "open" when unset. The Updates section is seeded with the
// first timestamped ### subheading so append callers can assume the
// section always exists (even when no subsequent updates have landed).
func (s *Store) LogFinding(in LogFindingInput) (string, error) {
	if strings.TrimSpace(in.Title) == "" {
		return "", fmt.Errorf("title is required")
	}
	if !isValidCategory(in.Category) {
		return "", fmt.Errorf("category must be one of %v, got %q", ValidFindingCategories, in.Category)
	}
	status := in.Status
	if status == "" {
		status = FindingStatusOpen
	}
	if !isValidStatus(status) {
		return "", fmt.Errorf("status must be one of %v, got %q", ValidFindingStatuses, status)
	}
	if err := s.ensureSeeded(); err != nil {
		return "", err
	}

	now := s.now()
	seed := formatUpdate(now, in.Author, status, "filed")
	e := Entry{
		Kind:     KindFinding,
		Title:    in.Title,
		Date:     now,
		Status:   status,
		Category: in.Category,
		Tags:     in.Tags,
		Related:  in.Related,
		Sections: map[string]string{
			"Summary": in.Summary,
			"Details": in.Details,
			"Updates": seed,
		},
	}
	return s.writeEntry(e, "findings")
}

// AppendToFinding reads an existing finding, appends a timestamped
// subheading to its Updates section, optionally transitions its status,
// and atomically rewrites the file. Concurrent callers serialize via
// flock on the target file; atomicity is via temp-then-rename.
//
// Returns the absolute path and the new status (unchanged if in.Status
// was empty). Err is non-nil if the slug doesn't exist.
func (s *Store) AppendToFinding(in AppendFindingInput) (string, string, error) {
	slug := strings.TrimSuffix(in.Slug, ".md")
	if !isValidFilenameSlug(slug) {
		return "", "", fmt.Errorf("invalid slug: %q (must be non-empty and contain only [a-z0-9-])", slug)
	}
	if strings.TrimSpace(in.Note) == "" {
		return "", "", fmt.Errorf("note is required")
	}
	if in.Status != "" && !isValidStatus(in.Status) {
		return "", "", fmt.Errorf("status must be one of %v, got %q", ValidFindingStatuses, in.Status)
	}

	path := filepath.Join(s.ContextDir(), "findings", slug+".md")

	// Verify the finding exists before acquiring the lock — we want
	// "not found" errors to surface without side-effects (lock file
	// creation).
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("finding not found: %s", slug)
		}
		return "", "", fmt.Errorf("stat finding: %w", err)
	}

	// Serialize concurrent appends via flock on a sidecar lock file.
	// We deliberately DON'T flock the target itself: the atomic
	// temp-then-rename write below unlinks the original inode, which
	// would strand flocks held by other writers (they'd be locking a
	// dead inode while a new inode occupies the path). The lock file
	// is never renamed, so every writer agrees on the same inode.
	lockPath := path + ".lock"
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return "", "", fmt.Errorf("opening lock file: %w", err)
	}
	defer func() { _ = lf.Close() }()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return "", "", fmt.Errorf("locking finding: %w", err)
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN) //nolint:errcheck

	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("reading finding: %w", err)
	}
	e, err := ParseEntry(string(raw))
	if err != nil {
		return "", "", fmt.Errorf("parsing finding %s: %w", slug, err)
	}
	if e.Kind != KindFinding {
		return "", "", fmt.Errorf("slug %q is a %s, not a finding", slug, e.Kind)
	}

	newStatus := e.Status
	if in.Status != "" {
		newStatus = in.Status
	}
	updateLine := formatUpdate(s.now(), in.Author, newStatus, in.Note)
	existing := strings.TrimSpace(e.Sections["Updates"])
	if existing == "" {
		e.Sections["Updates"] = updateLine
	} else {
		e.Sections["Updates"] = existing + "\n\n" + updateLine
	}
	e.Status = newStatus
	e.Slug = slug

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(e.Render()), 0644); err != nil {
		return "", "", fmt.Errorf("writing temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", "", fmt.Errorf("renaming temp: %w", err)
	}
	return path, newStatus, nil
}

// ReadFinding returns a single finding by slug. Unlike ReadEntries (which
// does a range scan), this is a direct per-slug lookup. Returns an error
// wrapping os.ErrNotExist when the file doesn't exist.
func (s *Store) ReadFinding(slug string) (Entry, error) {
	slug = strings.TrimSuffix(slug, ".md")
	if !isValidFilenameSlug(slug) {
		return Entry{}, fmt.Errorf("invalid slug: %q (must be non-empty and contain only [a-z0-9-])", slug)
	}
	path := filepath.Join(s.ContextDir(), "findings", slug+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Entry{}, fmt.Errorf("finding not found: %s: %w", slug, os.ErrNotExist)
		}
		return Entry{}, err
	}
	e, err := ParseEntry(string(raw))
	if err != nil {
		return Entry{}, fmt.Errorf("parsing finding %s: %w", slug, err)
	}
	e.Kind = KindFinding
	e.Slug = slug
	return e, nil
}

// formatUpdate builds a single "### <ts> — @author — status: <status>\n<note>"
// block for the Updates section. Author defaults to "agent" when empty.
func formatUpdate(ts time.Time, author, status, note string) string {
	who := author
	if strings.TrimSpace(who) == "" {
		who = "agent"
	}
	stamp := ts.UTC().Format("2006-01-02T15:04:05Z")
	note = strings.TrimSpace(note)
	return fmt.Sprintf("### %s — @%s — status: %s\n%s", stamp, who, status, note)
}

func isValidStatus(s string) bool {
	for _, v := range ValidFindingStatuses {
		if s == v {
			return true
		}
	}
	return false
}

func isValidCategory(s string) bool {
	for _, v := range ValidFindingCategories {
		if s == v {
			return true
		}
	}
	return false
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

	// Two cspace containers sharing .cspace/context/ via bind mount can
	// both reach here for the same title concurrently, both resolve
	// to the same filename (neither sees the other's write yet), and
	// an unguarded os.WriteFile would have one clobber the other.
	// O_EXCL creates the file atomically or fails with EEXIST; on
	// EEXIST we mark that name taken and retry collision resolution.
	// The bound is tight — the same title is unlikely to produce more
	// than a handful of collisions even under heavy parallelism.
	const maxRetries = 50
	for i := 0; i < maxRetries; i++ {
		path := filepath.Join(dir, resolved+".md")
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err == nil {
			_, werr := f.Write([]byte(e.Render()))
			cerr := f.Close()
			if werr != nil {
				_ = os.Remove(path)
				return "", werr
			}
			if cerr != nil {
				_ = os.Remove(path)
				return "", cerr
			}
			e.Slug = resolved
			return path, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
		// Collision with a concurrent writer since our scan. Mark the
		// conflicting name taken and bump the suffix.
		taken[resolved+".md"] = true
		resolved = ResolveCollision(base, taken)
	}
	return "", fmt.Errorf("giving up after %d collision retries for base %q", maxRetries, base)
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

// ensureSeeded creates .cspace/context/ and seeds human-owned files if missing.
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
// Zero Kind means all kinds. Since/Until are inclusive when set.
// Status, Category, and Tags apply only when Kind == KindFinding (or
// unset and the finding kind is included); they're silently ignored
// for decisions/discoveries.
type ListOptions struct {
	Kind  Kind
	Since *time.Time
	Until *time.Time
	Limit int // 0 = no limit

	// Finding-specific filters. Empty = no filter on that dimension.
	Status   []string // match if entry.Status is in the list
	Category []string // match if entry.Category is in the list
	Tags     []string // match if any entry.Tag is in the list (intersection)
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
	kinds := []Kind{KindDecision, KindDiscovery, KindFinding}
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
			// Finding-only filters — silently skip for other kinds, which
			// never have Status/Category/Tags populated.
			if k == KindFinding {
				if !matchesAny(e.Status, opts.Status) ||
					!matchesAny(e.Category, opts.Category) ||
					!tagsIntersect(e.Tags, opts.Tags) {
					continue
				}
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

// matchesAny returns true if filter is empty OR value is in filter.
// Empty filter = "no restriction on this dimension."
func matchesAny(value string, filter []string) bool {
	if len(filter) == 0 {
		return true
	}
	for _, f := range filter {
		if value == f {
			return true
		}
	}
	return false
}

// tagsIntersect returns true if filter is empty OR any entry.Tag is in filter.
func tagsIntersect(tags, filter []string) bool {
	if len(filter) == 0 {
		return true
	}
	for _, t := range tags {
		for _, f := range filter {
			if t == f {
				return true
			}
		}
	}
	return false
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
	case KindFinding:
		return "findings"
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
