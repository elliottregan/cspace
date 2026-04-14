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
)

// Entry is a decision or discovery.
type Entry struct {
	Kind     Kind
	Title    string
	Date     time.Time
	Slug     string            // filename slug without date prefix or extension (populated on read)
	Sections map[string]string // ordered by SectionOrder when rendered
}

// SectionOrder returns the canonical section names for a kind, in render order.
func SectionOrder(kind Kind) []string {
	switch kind {
	case KindDecision:
		return []string{"Context", "Alternatives", "Decision", "Consequences"}
	case KindDiscovery:
		return []string{"Finding", "Impact"}
	default:
		return nil
	}
}

// Render returns the markdown-with-frontmatter serialization.
func (e Entry) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: %s\ndate: %s\nkind: %s\n---\n\n",
		e.Title, e.Date.Format("2006-01-02"), e.Kind)
	for _, name := range SectionOrder(e.Kind) {
		body := strings.TrimSpace(e.Sections[name])
		fmt.Fprintf(&b, "## %s\n%s\n\n", name, body)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// ParseEntry parses a markdown-with-frontmatter blob back into an Entry.
// Intentionally minimal: expects the exact format Render produces.
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
