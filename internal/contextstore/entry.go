package contextstore

import (
	"bufio"
	"fmt"
	"strings"
	"time"
)

// Kind is the category of an agent-written entry.
type Kind string

const (
	KindDecision  Kind = "decision"
	KindDiscovery Kind = "discovery"
	// KindFinding is a lifecycle-aware entry: bugs, observations, and
	// refactor proposals. Unlike decisions and discoveries (which are
	// terminal records), findings accumulate updates over time and carry
	// a status field that transitions through a simple lifecycle.
	KindFinding Kind = "finding"
)

// FindingStatus values — not a strict state machine; any→any transitions
// are allowed so the tool surface stays minimal.
const (
	FindingStatusOpen         = "open"
	FindingStatusAcknowledged = "acknowledged"
	FindingStatusResolved     = "resolved"
	FindingStatusWontfix      = "wontfix"
)

// FindingCategory values.
const (
	FindingCategoryBug         = "bug"
	FindingCategoryObservation = "observation"
	FindingCategoryRefactor    = "refactor"
)

// ValidFindingStatuses is the lifecycle allowlist, also usable by callers
// that want to list/filter by status without hardcoding strings.
var ValidFindingStatuses = []string{
	FindingStatusOpen,
	FindingStatusAcknowledged,
	FindingStatusResolved,
	FindingStatusWontfix,
}

// ValidFindingCategories is the category allowlist.
var ValidFindingCategories = []string{
	FindingCategoryBug,
	FindingCategoryObservation,
	FindingCategoryRefactor,
}

// Entry is a decision, discovery, or finding.
//
// Status, Category, Tags, and Related are only populated for KindFinding
// entries; they're rendered/parsed when non-empty and silently ignored
// on other kinds. Decisions and discoveries remain exactly as they were.
type Entry struct {
	Kind     Kind
	Title    string
	Date     time.Time
	Slug     string            // filename slug without date prefix or extension (populated on read)
	Sections map[string]string // ordered by SectionOrder when rendered

	// Findings-only fields. Empty/nil for other kinds.
	Status   string   // one of ValidFindingStatuses (default: open on create)
	Category string   // one of ValidFindingCategories
	Tags     []string // free-form agent tags
	Related  []string // slugs or URLs for cross-reference
}

// SectionOrder returns the canonical section names for a kind, in render order.
func SectionOrder(kind Kind) []string {
	switch kind {
	case KindDecision:
		return []string{"Context", "Alternatives", "Decision", "Consequences"}
	case KindDiscovery:
		return []string{"Finding", "Impact"}
	case KindFinding:
		// Uniform across bug/observation/refactor for v1. If per-category
		// divergence shows value later, route via FindingCategory here.
		return []string{"Summary", "Details", "Updates"}
	default:
		return nil
	}
}

// Render returns the markdown-with-frontmatter serialization.
func (e Entry) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: %s\ndate: %s\nkind: %s\n",
		e.Title, e.Date.Format("2006-01-02"), e.Kind)
	// Finding-only frontmatter. Emitted only when non-empty so existing
	// decisions/discoveries round-trip unchanged.
	if e.Status != "" {
		fmt.Fprintf(&b, "status: %s\n", e.Status)
	}
	if e.Category != "" {
		fmt.Fprintf(&b, "category: %s\n", e.Category)
	}
	if len(e.Tags) > 0 {
		fmt.Fprintf(&b, "tags: %s\n", strings.Join(e.Tags, ", "))
	}
	if len(e.Related) > 0 {
		fmt.Fprintf(&b, "related: %s\n", strings.Join(e.Related, ", "))
	}
	b.WriteString("---\n\n")
	for _, name := range SectionOrder(e.Kind) {
		body := strings.TrimSpace(e.Sections[name])
		fmt.Fprintf(&b, "## %s\n%s\n\n", name, body)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// ParseEntry parses a markdown-with-frontmatter blob back into an Entry.
// Intentionally minimal: expects the exact format Render produces.
// Unknown frontmatter keys are ignored, so older entries without the
// finding-only fields still load cleanly.
func ParseEntry(raw string) (Entry, error) {
	s := bufio.NewScanner(strings.NewReader(raw))
	s.Buffer(make([]byte, 0, 64*1024), 1<<20)

	if !s.Scan() || s.Text() != "---" {
		return Entry{}, fmt.Errorf("missing opening frontmatter delimiter")
	}

	e := Entry{Sections: map[string]string{}}
	for s.Scan() {
		line := s.Text()
		if line == "---" {
			break
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			return Entry{}, fmt.Errorf("bad frontmatter line: %q", line)
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "title":
			e.Title = val
		case "date":
			t, err := time.Parse("2006-01-02", val)
			if err != nil {
				return Entry{}, fmt.Errorf("bad date: %w", err)
			}
			e.Date = t
		case "kind":
			e.Kind = Kind(val)
		case "status":
			e.Status = val
		case "category":
			e.Category = val
		case "tags":
			e.Tags = splitCommaList(val)
		case "related":
			e.Related = splitCommaList(val)
		}
	}

	var currentSection string
	var buf []string
	flush := func() {
		if currentSection != "" {
			e.Sections[currentSection] = strings.TrimSpace(strings.Join(buf, "\n"))
		}
		buf = buf[:0]
	}
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, "## ") {
			flush()
			currentSection = strings.TrimPrefix(line, "## ")
			continue
		}
		buf = append(buf, line)
	}
	flush()

	if err := s.Err(); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// splitCommaList splits "a, b,c ,d" into []string{"a","b","c","d"}. Empty
// input returns nil (so a missing frontmatter key and an explicit empty
// list are indistinguishable — intentional; prevents stray empties).
func splitCommaList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
