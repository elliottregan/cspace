# Context MCP Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a `cspace context-server` stdio MCP server that exposes `docs/context/` (direction/principles/roadmap + agent-logged decisions/discoveries) as a tool interface, and wire it into both host and container Claude sessions.

**Architecture:** Pure Go. File-layer in a new `internal/contextstore` package (no MCP dependency). MCP surface in `internal/cli/context_server.go` using `github.com/modelcontextprotocol/go-sdk`. Coordinator playbook calls `read_context` at dispatch time and injects direction+roadmap into sub-agent prompts via the existing `${STRATEGIC_CONTEXT_PREAMBLE}` slot. Implementer playbook pulls decisions/discoveries on demand and logs new entries.

**Tech Stack:** Go 1.25, `github.com/modelcontextprotocol/go-sdk` v1.5.0, Cobra (existing).

**Reference spec:** `docs/superpowers/specs/2026-04-13-context-mcp-server-design.md`

---

## File Structure

**New files:**
- `internal/contextstore/slug.go` — slug generation + collision resolution
- `internal/contextstore/entry.go` — entry types, frontmatter render/parse
- `internal/contextstore/store.go` — filesystem operations (read/write/list/delete, seeding)
- `internal/contextstore/slug_test.go`
- `internal/contextstore/entry_test.go`
- `internal/contextstore/store_test.go`
- `internal/cli/context_server.go` — `cspace context-server` command, MCP tool registration
- `internal/cli/context_server_test.go` — in-memory MCP client/server E2E test
- `.mcp.json` — host MCP config, registers `cspace-context` for every `claude` session in the repo

**Modified files:**
- `go.mod` / `go.sum` — add `github.com/modelcontextprotocol/go-sdk`
- `internal/cli/root.go` — register `newContextServerCmd()`, skip config loading for it
- `lib/scripts/init-claude-plugins.sh` — register `cspace-context` MCP server (like playwright)
- `lib/agents/coordinator.md` — use `read_context` to build the dispatch preamble
- `lib/agents/implementer.md` — document `read_context` / `log_decision` / `log_discovery`
- `CLAUDE.md` — add a short "Project Context" section pointing at `docs/context/`

**Removed files (replaces old `sync-context` flow):**
- `internal/cli/sync_context.go`
- References to `sync-context` in `internal/cli/root.go`
- `docs/src/content/docs/cli-reference/instance-management.md` — `cspace sync-context` section
- `docs/src/content/docs/cli-reference/overview.md` — `cspace sync-context` table row

---

## Task 1: Slug generation

**Files:**
- Create: `internal/contextstore/slug.go`
- Test: `internal/contextstore/slug_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/contextstore/slug_test.go
package contextstore

import "testing"

func TestSlugify(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Use Go MCP SDK", "use-go-mcp-sdk"},
		{"  Trim & Collapse!!  ", "trim-collapse"},
		{"Multiple   spaces---and_underscores", "multiple-spaces-and-underscores"},
		{"UPPER/lower:Mixed", "upper-lower-mixed"},
		{"", ""},
		{"!!!", ""},
	}
	for _, c := range cases {
		if got := Slugify(c.in); got != c.want {
			t.Errorf("Slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSlugifyTruncates(t *testing.T) {
	long := "a-very-long-title-that-exceeds-the-sixty-character-limit-imposed-by-the-slug-function"
	got := Slugify(long)
	if len(got) > 60 {
		t.Errorf("Slugify length = %d, want <= 60", len(got))
	}
	if got[len(got)-1] == '-' {
		t.Errorf("Slugify trailing hyphen: %q", got)
	}
}

func TestResolveCollision(t *testing.T) {
	taken := map[string]bool{
		"2026-04-13-foo.md":   true,
		"2026-04-13-foo-2.md": true,
	}
	got := ResolveCollision("2026-04-13-foo", taken)
	if got != "2026-04-13-foo-3" {
		t.Errorf("ResolveCollision = %q, want 2026-04-13-foo-3", got)
	}

	got = ResolveCollision("2026-04-13-bar", taken)
	if got != "2026-04-13-bar" {
		t.Errorf("ResolveCollision = %q, want 2026-04-13-bar", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/contextstore/...`
Expected: FAIL — package does not compile (Slugify, ResolveCollision undefined).

- [ ] **Step 3: Implement slug.go**

```go
// internal/contextstore/slug.go
package contextstore

import (
	"fmt"
	"strings"
	"unicode"
)

const maxSlugLen = 60

// Slugify converts a title into a filesystem-safe slug:
// lowercase, non-alphanumeric runs collapsed to single hyphens,
// truncated to maxSlugLen with trailing hyphens trimmed.
func Slugify(title string) string {
	var b strings.Builder
	b.Grow(len(title))
	prevHyphen := true // suppresses leading hyphen
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevHyphen = false
			continue
		}
		if !prevHyphen {
			b.WriteByte('-')
			prevHyphen = true
		}
	}
	s := strings.TrimRight(b.String(), "-")
	if len(s) > maxSlugLen {
		s = strings.TrimRight(s[:maxSlugLen], "-")
	}
	return s
}

// ResolveCollision returns base if "<base>.md" is not in taken,
// otherwise appends -2, -3, ... until an unused name is found.
func ResolveCollision(base string, taken map[string]bool) string {
	if !taken[base+".md"] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !taken[candidate+".md"] {
			return candidate
		}
	}
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/contextstore/...`
Expected: PASS (all three tests).

- [ ] **Step 5: Commit**

```bash
git add internal/contextstore/slug.go internal/contextstore/slug_test.go
git commit -m "contextstore: slug generation and collision resolution"
```

---

## Task 2: Entry types and frontmatter

**Files:**
- Create: `internal/contextstore/entry.go`
- Test: `internal/contextstore/entry_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/contextstore/entry_test.go
package contextstore

import (
	"strings"
	"testing"
	"time"
)

func TestRenderDecision(t *testing.T) {
	e := Entry{
		Kind:  KindDecision,
		Title: "Use Go MCP SDK",
		Date:  time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC),
		Sections: map[string]string{
			"Context":      "needed a server",
			"Alternatives": "Node SDK",
			"Decision":     "Go SDK",
			"Consequences": "Go module dep",
		},
	}
	got := e.Render()
	want := "---\ntitle: Use Go MCP SDK\ndate: 2026-04-13\nkind: decision\n---\n\n" +
		"## Context\nneeded a server\n\n" +
		"## Alternatives\nNode SDK\n\n" +
		"## Decision\nGo SDK\n\n" +
		"## Consequences\nGo module dep\n"
	if got != want {
		t.Errorf("Render mismatch:\n--got--\n%s\n--want--\n%s", got, want)
	}
}

func TestRenderDiscovery(t *testing.T) {
	e := Entry{
		Kind:  KindDiscovery,
		Title: "Firewall blocks foo",
		Date:  time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC),
		Sections: map[string]string{
			"Finding": "port blocked",
			"Impact":  "must allowlist",
		},
	}
	got := e.Render()
	if !strings.Contains(got, "kind: discovery") {
		t.Errorf("missing discovery kind in frontmatter: %s", got)
	}
	if !strings.Contains(got, "## Finding\nport blocked") {
		t.Errorf("missing Finding section: %s", got)
	}
}

func TestParseEntry(t *testing.T) {
	raw := "---\ntitle: Use Go MCP SDK\ndate: 2026-04-13\nkind: decision\n---\n\n" +
		"## Context\nneeded a server\n\n## Decision\nGo SDK\n"
	e, err := ParseEntry(raw)
	if err != nil {
		t.Fatalf("ParseEntry: %v", err)
	}
	if e.Title != "Use Go MCP SDK" {
		t.Errorf("Title = %q", e.Title)
	}
	if e.Kind != KindDecision {
		t.Errorf("Kind = %q", e.Kind)
	}
	if e.Date.Format("2006-01-02") != "2026-04-13" {
		t.Errorf("Date = %v", e.Date)
	}
	if e.Sections["Context"] != "needed a server" {
		t.Errorf("Context section = %q", e.Sections["Context"])
	}
	if e.Sections["Decision"] != "Go SDK" {
		t.Errorf("Decision section = %q", e.Sections["Decision"])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/contextstore/...`
Expected: FAIL — Entry, KindDecision, KindDiscovery, ParseEntry undefined.

- [ ] **Step 3: Implement entry.go**

```go
// internal/contextstore/entry.go
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
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/contextstore/...`
Expected: PASS (all slug tests + three entry tests).

- [ ] **Step 5: Commit**

```bash
git add internal/contextstore/entry.go internal/contextstore/entry_test.go
git commit -m "contextstore: entry types and markdown frontmatter render/parse"
```

---

## Task 3: Store — write decisions and discoveries, seed on first write

**Files:**
- Create: `internal/contextstore/store.go`
- Test: `internal/contextstore/store_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/contextstore/store_test.go
package contextstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return &Store{Root: dir, Now: func() time.Time { return time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC) }}
}

func TestLogDecisionWritesFileAndSeeds(t *testing.T) {
	s := newStore(t)

	path, err := s.LogDecision(LogDecisionInput{
		Title:        "Use Go MCP SDK",
		Context:      "needed a server",
		Alternatives: "Node SDK",
		Decision:     "Go SDK",
		Consequences: "Go module dep",
	})
	if err != nil {
		t.Fatalf("LogDecision: %v", err)
	}

	want := filepath.Join(s.Root, "docs/context/decisions/2026-04-13-use-go-mcp-sdk.md")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(body), "kind: decision") {
		t.Errorf("missing kind: %s", body)
	}

	// Seeding side effect: direction/principles/roadmap created.
	for _, name := range []string{"direction.md", "principles.md", "roadmap.md"} {
		p := filepath.Join(s.Root, "docs/context", name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("seed missing: %v", err)
		}
	}
}

func TestLogDiscoveryAddsCollisionSuffix(t *testing.T) {
	s := newStore(t)

	first, err := s.LogDiscovery(LogDiscoveryInput{Title: "Firewall", Finding: "x", Impact: "y"})
	if err != nil {
		t.Fatalf("LogDiscovery 1: %v", err)
	}
	second, err := s.LogDiscovery(LogDiscoveryInput{Title: "Firewall", Finding: "x", Impact: "y"})
	if err != nil {
		t.Fatalf("LogDiscovery 2: %v", err)
	}
	if filepath.Base(first) != "2026-04-13-firewall.md" {
		t.Errorf("first = %s", first)
	}
	if filepath.Base(second) != "2026-04-13-firewall-2.md" {
		t.Errorf("second = %s", second)
	}
}

func TestSeedOnlyRunsOnce(t *testing.T) {
	s := newStore(t)

	// User edits direction.md after first seed.
	if _, err := s.LogDiscovery(LogDiscoveryInput{Title: "T", Finding: "f", Impact: "i"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.Root, "docs/context/direction.md")
	if err := os.WriteFile(dir, []byte("custom direction"), 0644); err != nil {
		t.Fatal(err)
	}

	// Second write must not overwrite direction.md.
	if _, err := s.LogDiscovery(LogDiscoveryInput{Title: "U", Finding: "f", Impact: "i"}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dir)
	if string(got) != "custom direction" {
		t.Errorf("direction.md was overwritten: %q", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/contextstore/...`
Expected: FAIL — Store, LogDecisionInput, etc. undefined.

- [ ] **Step 3: Implement store.go (write + seed)**

```go
// internal/contextstore/store.go
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

// Human-owned seed templates. Comment text uses HTML comments so it is hidden
// when rendered but visible when editing. Only written if the file is missing.
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
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/contextstore/...`
Expected: PASS (all previous tests + three new store tests).

- [ ] **Step 5: Commit**

```bash
git add internal/contextstore/store.go internal/contextstore/store_test.go
git commit -m "contextstore: log decisions/discoveries, seed human-owned files"
```

---

## Task 4: Store — read, list, remove

**Files:**
- Modify: `internal/contextstore/store.go` (append)
- Modify: `internal/contextstore/store_test.go` (append)

- [ ] **Step 1: Append failing tests**

Append to `internal/contextstore/store_test.go`:

```go
func TestListEntriesFiltersKindAndDate(t *testing.T) {
	s := newStore(t)

	s.Now = func() time.Time { return time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC) }
	_, _ = s.LogDecision(LogDecisionInput{Title: "old", Context: "c", Decision: "d"})

	s.Now = func() time.Time { return time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC) }
	_, _ = s.LogDecision(LogDecisionInput{Title: "new decision", Decision: "d"})
	_, _ = s.LogDiscovery(LogDiscoveryInput{Title: "new discovery", Finding: "f"})

	all, err := s.ListEntries(ListOptions{})
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("ListEntries got %d, want 3", len(all))
	}

	decisions, _ := s.ListEntries(ListOptions{Kind: KindDecision})
	if len(decisions) != 2 {
		t.Errorf("decisions got %d, want 2", len(decisions))
	}

	since := time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC)
	recent, _ := s.ListEntries(ListOptions{Since: &since})
	if len(recent) != 2 {
		t.Errorf("recent got %d, want 2 (new decision + new discovery)", len(recent))
	}
}

func TestReadEntriesReturnsBodies(t *testing.T) {
	s := newStore(t)
	_, _ = s.LogDiscovery(LogDiscoveryInput{Title: "Firewall", Finding: "blocked", Impact: "allowlist"})

	entries, err := s.ReadEntries(ListOptions{Kind: KindDiscovery})
	if err != nil {
		t.Fatalf("ReadEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d", len(entries))
	}
	if entries[0].Sections["Finding"] != "blocked" {
		t.Errorf("Finding = %q", entries[0].Sections["Finding"])
	}
}

func TestReadHumanFilesReturnsSeedsWhenPresent(t *testing.T) {
	s := newStore(t)
	if err := s.ensureSeeded(); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadHuman("direction")
	if err != nil {
		t.Fatalf("ReadHuman: %v", err)
	}
	if !strings.Contains(got, "# Direction") {
		t.Errorf("unexpected content: %q", got)
	}

	if _, err := s.ReadHuman("nonexistent"); err == nil {
		t.Error("expected error for unknown section")
	}
}

func TestRemoveEntry(t *testing.T) {
	s := newStore(t)
	path, _ := s.LogDiscovery(LogDiscoveryInput{Title: "Zap", Finding: "f", Impact: "i"})

	if err := s.RemoveEntry(KindDiscovery, "2026-04-13-zap"); err != nil {
		t.Fatalf("RemoveEntry: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still exists: %v", err)
	}

	if err := s.RemoveEntry(KindDiscovery, "does-not-exist"); err == nil {
		t.Error("expected error for missing slug")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/contextstore/...`
Expected: FAIL — ListEntries, ReadEntries, ReadHuman, RemoveEntry, ListOptions, EntrySummary undefined.

- [ ] **Step 3: Append read/list/remove to store.go**

Append to `internal/contextstore/store.go`:

```go
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

// ListEntries returns metadata for matching entries, newest first.
func (s *Store) ListEntries(opts ListOptions) ([]EntrySummary, error) {
	kinds := []Kind{KindDecision, KindDiscovery}
	if opts.Kind != "" {
		kinds = []Kind{opts.Kind}
	}
	var out []EntrySummary
	for _, k := range kinds {
		subdir := subdirFor(k)
		dir := filepath.Join(s.ContextDir(), subdir)
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
			slug := strings.TrimSuffix(de.Name(), ".md")
			out = append(out, EntrySummary{
				Kind:  k,
				Date:  e.Date,
				Slug:  slug,
				Title: e.Title,
				Path:  path,
			})
		}
	}
	// Newest first; stable by slug for same date.
	sortSummaries(out)
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

// ReadEntries returns full entry bodies matching opts, newest first.
func (s *Store) ReadEntries(opts ListOptions) ([]Entry, error) {
	summaries, err := s.ListEntries(opts)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(summaries))
	for _, sum := range summaries {
		raw, err := os.ReadFile(sum.Path)
		if err != nil {
			return nil, err
		}
		e, err := ParseEntry(string(raw))
		if err != nil {
			continue
		}
		e.Slug = sum.Slug
		out = append(out, e)
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
		return "", nil // treat missing as empty; the server is always usable
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// RemoveEntry deletes an entry by slug. Slug may include or omit the .md suffix.
func (s *Store) RemoveEntry(kind Kind, slug string) error {
	slug = strings.TrimSuffix(slug, ".md")
	path := filepath.Join(s.ContextDir(), subdirFor(kind), slug+".md")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("entry not found: %s/%s", kind, slug)
	}
	return os.Remove(path)
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

func sortSummaries(s []EntrySummary) {
	// stable sort: date desc, then slug asc
	for i := 1; i < len(s); i++ {
		j := i
		for j > 0 && lessSummary(s[j], s[j-1]) {
			s[j], s[j-1] = s[j-1], s[j]
			j--
		}
	}
}

func lessSummary(a, b EntrySummary) bool {
	if !a.Date.Equal(b.Date) {
		return a.Date.After(b.Date)
	}
	return a.Slug < b.Slug
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/contextstore/...`
Expected: PASS (all prior tests + four new ones).

- [ ] **Step 5: Commit**

```bash
git add internal/contextstore/store.go internal/contextstore/store_test.go
git commit -m "contextstore: list, read, and remove entries"
```

---

## Task 5: Add MCP SDK dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the module**

Run: `go get github.com/modelcontextprotocol/go-sdk@v1.5.0`

- [ ] **Step 2: Verify module resolves**

Run: `go mod download github.com/modelcontextprotocol/go-sdk`
Expected: no output, no error.

- [ ] **Step 3: Run full test suite + vet**

Run: `make test && make vet`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add modelcontextprotocol/go-sdk for context server"
```

---

## Task 6: `cspace context-server` command scaffold

**Files:**
- Create: `internal/cli/context_server.go`
- Modify: `internal/cli/root.go`

- [ ] **Step 1: Write the command file**

```go
// internal/cli/context_server.go
package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/elliottregan/cspace/internal/contextstore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func newContextServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context-server",
		Short: "Run the docs/context MCP server over stdio",
		Long: `Expose docs/context/ (direction, principles, roadmap, decisions, discoveries)
as an MCP server over stdio. Typically invoked by Claude Code via .mcp.json
or the container's Claude MCP config, not by humans directly.`,
		GroupID: "other",
		RunE:    runContextServer,
	}
	cmd.Flags().String("root", "", "Project root (default: current working directory)")
	return cmd
}

func runContextServer(cmd *cobra.Command, args []string) error {
	root, _ := cmd.Flags().GetString("root")
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		root = cwd
	}

	store := &contextstore.Store{Root: root}
	server := mcp.NewServer(&mcp.Implementation{Name: "cspace-context", Version: Version}, nil)
	registerContextTools(server, store)

	return server.Run(context.Background(), &mcp.StdioTransport{})
}

// registerContextTools is exported to the package so tests can register against
// an in-memory server. Implemented in task 7.
func registerContextTools(server *mcp.Server, store *contextstore.Store) {
	// populated in task 7
	_ = server
	_ = store
}
```

- [ ] **Step 2: Register the command in root.go**

Edit `internal/cli/root.go`:

1. Add `newContextServerCmd(),` inside the "Other" group in the `root.AddCommand(...)` list (next to `newVersionCmd()`).
2. Add `"context-server"` to the `switch cmd.Name()` case that skips config/asset loading (next to `"version"`, `"help"`, `"completion"`, `"init"`, `"self-update"`).

The relevant edit to the switch (around line 37):

```go
switch cmd.Name() {
case "version", "help", "completion", "init", "self-update", "context-server":
    return nil
}
```

And to `root.AddCommand` (around line 112), add `newContextServerCmd(),` alongside `newSyncContextCmd()`.

- [ ] **Step 3: Verify it builds and the command appears**

Run: `make build && ./bin/cspace-go context-server --help`
Expected: help text prints, no error.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/context_server.go internal/cli/root.go
git commit -m "cli: scaffold cspace context-server subcommand"
```

---

## Task 7: Register MCP tools

**Files:**
- Modify: `internal/cli/context_server.go`

- [ ] **Step 1: Replace the `registerContextTools` stub with the full implementation**

```go
// registerContextTools registers all five context tools on server.
func registerContextTools(server *mcp.Server, store *contextstore.Store) {
	mcp.AddTool[readContextArgs, readContextOut](server, &mcp.Tool{
		Name:        "read_context",
		Description: "Read the project brain: direction, principles, roadmap, and recent decisions/discoveries. Returns everything by default; filter with `sections`.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in readContextArgs) (*mcp.CallToolResult, readContextOut, error) {
		out, err := handleReadContext(store, in)
		if err != nil {
			return nil, readContextOut{}, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: out.Render()}},
		}, out, nil
	})

	mcp.AddTool[logDecisionArgs, logEntryOut](server, &mcp.Tool{
		Name:        "log_decision",
		Description: "Record a significant architectural or design decision. Only log things that would save a future session time.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in logDecisionArgs) (*mcp.CallToolResult, logEntryOut, error) {
		path, err := store.LogDecision(contextstore.LogDecisionInput{
			Title: in.Title, Context: in.Context, Alternatives: in.Alternatives,
			Decision: in.Decision, Consequences: in.Consequences,
		})
		if err != nil {
			return nil, logEntryOut{}, err
		}
		return textResult("logged decision: " + path), logEntryOut{Path: path}, nil
	})

	mcp.AddTool[logDiscoveryArgs, logEntryOut](server, &mcp.Tool{
		Name:        "log_discovery",
		Description: "Record something non-obvious learned about the code or infrastructure.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in logDiscoveryArgs) (*mcp.CallToolResult, logEntryOut, error) {
		path, err := store.LogDiscovery(contextstore.LogDiscoveryInput{
			Title: in.Title, Finding: in.Finding, Impact: in.Impact,
		})
		if err != nil {
			return nil, logEntryOut{}, err
		}
		return textResult("logged discovery: " + path), logEntryOut{Path: path}, nil
	})

	mcp.AddTool[listEntriesArgs, listEntriesOut](server, &mcp.Tool{
		Name:        "list_entries",
		Description: "List decisions and/or discoveries with optional date range. Returns metadata, no bodies.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listEntriesArgs) (*mcp.CallToolResult, listEntriesOut, error) {
		opts, err := in.toOptions()
		if err != nil {
			return nil, listEntriesOut{}, err
		}
		summaries, err := store.ListEntries(opts)
		if err != nil {
			return nil, listEntriesOut{}, err
		}
		out := listEntriesOut{Entries: make([]entrySummaryDTO, 0, len(summaries))}
		for _, s := range summaries {
			out.Entries = append(out.Entries, entrySummaryDTO{
				Kind: string(s.Kind), Date: s.Date.Format("2006-01-02"),
				Slug: s.Slug, Title: s.Title, Path: s.Path,
			})
		}
		return textResult(fmt.Sprintf("%d entries", len(out.Entries))), out, nil
	})

	mcp.AddTool[removeEntryArgs, any](server, &mcp.Tool{
		Name:        "remove_entry",
		Description: "Delete a decision or discovery by slug. For human curation passes.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in removeEntryArgs) (*mcp.CallToolResult, any, error) {
		kind := contextstore.Kind(in.Kind)
		if kind != contextstore.KindDecision && kind != contextstore.KindDiscovery {
			return nil, nil, fmt.Errorf("kind must be 'decision' or 'discovery'")
		}
		if err := store.RemoveEntry(kind, in.Slug); err != nil {
			return nil, nil, err
		}
		return textResult("removed " + in.Kind + "/" + in.Slug), nil, nil
	})
}

// --- Tool input / output types ---

type readContextArgs struct {
	Sections []string `json:"sections,omitempty" jsonschema:"subset of: direction, principles, roadmap, decisions, discoveries. Omit for all."`
	Since    string   `json:"since,omitempty" jsonschema:"ISO date (YYYY-MM-DD). Filters decisions and discoveries."`
	Limit    int      `json:"limit,omitempty" jsonschema:"max entries per kind. Default 20."`
}

type readContextOut struct {
	Direction   string             `json:"direction,omitempty"`
	Principles  string             `json:"principles,omitempty"`
	Roadmap     string             `json:"roadmap,omitempty"`
	Decisions   []entryDTO         `json:"decisions,omitempty"`
	Discoveries []entryDTO         `json:"discoveries,omitempty"`
}

type entryDTO struct {
	Date     string            `json:"date"`
	Slug     string            `json:"slug"`
	Title    string            `json:"title"`
	Sections map[string]string `json:"sections"`
}

func (o readContextOut) Render() string {
	var b strings.Builder
	if o.Direction != "" {
		b.WriteString("# direction.md\n\n")
		b.WriteString(o.Direction)
		b.WriteString("\n\n")
	}
	if o.Principles != "" {
		b.WriteString("# principles.md\n\n")
		b.WriteString(o.Principles)
		b.WriteString("\n\n")
	}
	if o.Roadmap != "" {
		b.WriteString("# roadmap.md\n\n")
		b.WriteString(o.Roadmap)
		b.WriteString("\n\n")
	}
	if len(o.Decisions) > 0 {
		b.WriteString("# decisions\n\n")
		for _, e := range o.Decisions {
			fmt.Fprintf(&b, "## %s (%s)\n\n", e.Title, e.Date)
			for _, name := range []string{"Context", "Alternatives", "Decision", "Consequences"} {
				if v := e.Sections[name]; v != "" {
					fmt.Fprintf(&b, "### %s\n%s\n\n", name, v)
				}
			}
		}
	}
	if len(o.Discoveries) > 0 {
		b.WriteString("# discoveries\n\n")
		for _, e := range o.Discoveries {
			fmt.Fprintf(&b, "## %s (%s)\n\n", e.Title, e.Date)
			for _, name := range []string{"Finding", "Impact"} {
				if v := e.Sections[name]; v != "" {
					fmt.Fprintf(&b, "### %s\n%s\n\n", name, v)
				}
			}
		}
	}
	return b.String()
}

type logDecisionArgs struct {
	Title        string `json:"title" jsonschema:"short description; becomes the slug"`
	Context      string `json:"context" jsonschema:"why this decision came up"`
	Alternatives string `json:"alternatives" jsonschema:"what else was considered"`
	Decision     string `json:"decision" jsonschema:"what was chosen"`
	Consequences string `json:"consequences" jsonschema:"what follows from this choice"`
}

type logDiscoveryArgs struct {
	Title   string `json:"title" jsonschema:"short description; becomes the slug"`
	Finding string `json:"finding" jsonschema:"what was found"`
	Impact  string `json:"impact" jsonschema:"why it matters for future work"`
}

type logEntryOut struct {
	Path string `json:"path"`
}

type listEntriesArgs struct {
	Kind  string `json:"kind,omitempty" jsonschema:"decisions | discoveries | both. Default: both."`
	Since string `json:"since,omitempty" jsonschema:"ISO date inclusive lower bound"`
	Until string `json:"until,omitempty" jsonschema:"ISO date inclusive upper bound"`
}

type listEntriesOut struct {
	Entries []entrySummaryDTO `json:"entries"`
}

type entrySummaryDTO struct {
	Kind  string `json:"kind"`
	Date  string `json:"date"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
	Path  string `json:"path"`
}

func (a listEntriesArgs) toOptions() (contextstore.ListOptions, error) {
	var opts contextstore.ListOptions
	switch a.Kind {
	case "", "both":
	case "decisions":
		opts.Kind = contextstore.KindDecision
	case "discoveries":
		opts.Kind = contextstore.KindDiscovery
	default:
		return opts, fmt.Errorf("kind must be decisions, discoveries, or both")
	}
	parseDate := func(s string) (*time.Time, error) {
		if s == "" {
			return nil, nil
		}
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return nil, err
		}
		return &t, nil
	}
	var err error
	if opts.Since, err = parseDate(a.Since); err != nil {
		return opts, fmt.Errorf("since: %w", err)
	}
	if opts.Until, err = parseDate(a.Until); err != nil {
		return opts, fmt.Errorf("until: %w", err)
	}
	return opts, nil
}

type removeEntryArgs struct {
	Kind string `json:"kind" jsonschema:"decision | discovery"`
	Slug string `json:"slug" jsonschema:"entry slug (filename without .md)"`
}

// handleReadContext builds the readContextOut, honoring section filters and limits.
func handleReadContext(store *contextstore.Store, in readContextArgs) (readContextOut, error) {
	want := map[string]bool{}
	if len(in.Sections) == 0 {
		for _, s := range []string{"direction", "principles", "roadmap", "decisions", "discoveries"} {
			want[s] = true
		}
	} else {
		for _, s := range in.Sections {
			want[s] = true
		}
	}

	var out readContextOut
	var err error
	if want["direction"] {
		if out.Direction, err = store.ReadHuman("direction"); err != nil {
			return out, err
		}
	}
	if want["principles"] {
		if out.Principles, err = store.ReadHuman("principles"); err != nil {
			return out, err
		}
	}
	if want["roadmap"] {
		if out.Roadmap, err = store.ReadHuman("roadmap"); err != nil {
			return out, err
		}
	}

	limit := in.Limit
	if limit == 0 {
		limit = 20
	}
	var since *time.Time
	if in.Since != "" {
		t, err := time.Parse("2006-01-02", in.Since)
		if err != nil {
			return out, fmt.Errorf("since: %w", err)
		}
		since = &t
	}

	toDTO := func(e contextstore.Entry) entryDTO {
		return entryDTO{
			Date: e.Date.Format("2006-01-02"), Slug: e.Slug,
			Title: e.Title, Sections: e.Sections,
		}
	}

	if want["decisions"] {
		es, err := store.ReadEntries(contextstore.ListOptions{Kind: contextstore.KindDecision, Since: since, Limit: limit})
		if err != nil {
			return out, err
		}
		for _, e := range es {
			out.Decisions = append(out.Decisions, toDTO(e))
		}
	}
	if want["discoveries"] {
		es, err := store.ReadEntries(contextstore.ListOptions{Kind: contextstore.KindDiscovery, Since: since, Limit: limit})
		if err != nil {
			return out, err
		}
		for _, e := range es {
			out.Discoveries = append(out.Discoveries, toDTO(e))
		}
	}
	return out, nil
}

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
```

- [ ] **Step 2: Update imports in context_server.go**

The file now needs these imports:

```go
import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/contextstore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)
```

- [ ] **Step 3: Verify it builds**

Run: `make build`
Expected: no compile errors.

Run: `make vet`
Expected: no issues.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/context_server.go
git commit -m "context-server: register read/log/list/remove MCP tools"
```

---

## Task 8: End-to-end test via in-memory transport

**Files:**
- Create: `internal/cli/context_server_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/cli/context_server_test.go
package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elliottregan/cspace/internal/contextstore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestContextServerE2E(t *testing.T) {
	root := t.TempDir()
	store := &contextstore.Store{Root: root}

	server := mcp.NewServer(&mcp.Implementation{Name: "cspace-context-test"}, nil)
	registerContextTools(server, store)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client"}, nil)
	serverT, clientT := mcp.NewInMemoryTransports()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = server.Run(ctx, serverT) }()

	session, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer session.Close()

	// log_decision
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "log_decision",
		Arguments: map[string]any{
			"title":        "Use Go MCP SDK",
			"context":      "needed a server",
			"alternatives": "Node SDK",
			"decision":     "Go SDK",
			"consequences": "Go module dep",
		},
	}); err != nil {
		t.Fatalf("log_decision: %v", err)
	}

	// file landed in docs/context/decisions/
	matches, _ := filepath.Glob(filepath.Join(root, "docs/context/decisions/*.md"))
	if len(matches) != 1 {
		t.Fatalf("want 1 decision file, got %d", len(matches))
	}
	body, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(body), "kind: decision") {
		t.Errorf("missing kind: %s", body)
	}

	// log_discovery
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "log_discovery",
		Arguments: map[string]any{
			"title": "Firewall", "finding": "blocked", "impact": "allowlist",
		},
	}); err != nil {
		t.Fatalf("log_discovery: %v", err)
	}

	// list_entries
	listRes, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "list_entries", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("list_entries: %v", err)
	}
	if len(listRes.Content) == 0 {
		t.Error("list_entries returned no content")
	}

	// read_context returns human seeds + logged entries
	readRes, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "read_context", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("read_context: %v", err)
	}
	text := toText(readRes)
	for _, must := range []string{"# direction.md", "# decisions", "# discoveries", "Use Go MCP SDK", "Firewall"} {
		if !strings.Contains(text, must) {
			t.Errorf("read_context output missing %q:\n%s", must, text)
		}
	}

	// remove_entry
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "remove_entry",
		Arguments: map[string]any{
			"kind": "discovery",
			"slug": strings.TrimSuffix(filepath.Base(mustGlobOne(t, root, "discoveries")), ".md"),
		},
	}); err != nil {
		t.Fatalf("remove_entry: %v", err)
	}
	if got, _ := filepath.Glob(filepath.Join(root, "docs/context/discoveries/*.md")); len(got) != 0 {
		t.Errorf("discovery not removed: %v", got)
	}
}

func toText(r *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func mustGlobOne(t *testing.T, root, subdir string) string {
	t.Helper()
	got, err := filepath.Glob(filepath.Join(root, "docs/context", subdir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 file in %s, got %d", subdir, len(got))
	}
	return got[0]
}
```

- [ ] **Step 2: Run it**

Run: `go test ./internal/cli/... -run TestContextServerE2E -v`
Expected: PASS. If anything fails, inspect the error and adjust — the most likely issue is an MCP SDK API mismatch (field or method renamed). Fix based on the compiler or runtime error; do NOT skip the test.

- [ ] **Step 3: Run full suite**

Run: `make test && make vet`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/context_server_test.go
git commit -m "context-server: in-memory MCP E2E test for all five tools"
```

---

## Task 9: Host wiring — `.mcp.json` at project root

**Files:**
- Create: `.mcp.json`

- [ ] **Step 1: Create the file**

```json
{
  "mcpServers": {
    "cspace-context": {
      "command": "cspace",
      "args": ["context-server"]
    }
  }
}
```

- [ ] **Step 2: Verify a host Claude session would pick it up**

Run: `cat .mcp.json | jq .`
Expected: valid JSON prints.

(No further local verification — this is consumed by Claude Code, not by cspace itself.)

- [ ] **Step 3: Commit**

```bash
git add .mcp.json
git commit -m "add .mcp.json to expose cspace-context to host claude sessions"
```

---

## Task 10: Container wiring — register `cspace-context` in init-claude-plugins.sh

**Files:**
- Modify: `lib/scripts/init-claude-plugins.sh`

- [ ] **Step 1: Add registration block**

Find the "Built-in browser MCP servers" section (around line 83) and append a new block *after* the `chrome-devtools` registration (around line 107) but *before* the `if [ -f "$MARKER_FILE" ]` check. Use the `chrome-devtools` registration as the template.

New block to append:

```bash
# cspace-context MCP — project context brain (docs/context/)
# Runs as a subprocess inside the agent container; file access is the workspace.
echo "  - cspace-context: registering"
sudo -u dev "$CLAUDE_BIN" mcp add --scope user cspace-context -- \
    cspace context-server --root /workspace 2>&1 | sed 's/^/      /' || true
```

The exact placement:

- Before the change, line 107 ends with `... 2>&1 | sed 's/^/      /' || true` (chrome-devtools).
- Insert the block above between `fi` (line 107) and the next `# Skip plugin init if already done` comment.

Note: this block must live *outside* the `if [ -n "$CSPACE_CONTAINER_NAME" ]` conditional because cspace-context does not depend on the browser sidecar.

- [ ] **Step 2: Rebuild and verify the script is syntactically valid**

Run: `bash -n lib/scripts/init-claude-plugins.sh`
Expected: no output, no error.

- [ ] **Step 3: Verify end-to-end on a warm container (manual)**

Run: `cspace rebuild && cspace warm mercury-test && cspace ssh mercury-test -- claude mcp list`
Expected: `cspace-context` appears in the list.

If a fresh cspace run isn't practical in this session, note the manual verification step for the human and proceed.

- [ ] **Step 4: Commit**

```bash
git add lib/scripts/init-claude-plugins.sh
git commit -m "init-claude-plugins: register cspace-context MCP for supervised agents"
```

---

## Task 11: Coordinator playbook — build preamble from `read_context`

**Files:**
- Modify: `lib/agents/coordinator.md`

- [ ] **Step 1: Replace the preamble-substitution section**

Find the existing block (around lines 67–91) that describes rendering `${STRATEGIC_CONTEXT_PREAMBLE}` with `sed -e "s|\${STRATEGIC_CONTEXT_PREAMBLE}||g"` and the paragraph explaining that multi-line preambles are awkward to inline.

Replace the entire "Render the prompt" section with:

````markdown
### Render the prompt

Before launching each agent, **build the strategic context preamble** by calling the `read_context` MCP tool (from the `cspace-context` server) for `direction` and `roadmap`:

```
# Pseudocode — the exact call depends on your MCP tool invocation syntax.
preamble = read_context(sections=["direction", "roadmap"])
```

Write the preamble to a file:

```bash
cat > /tmp/preamble-$N.md <<EOF
## Project Context

$preamble

_Call \`read_context\` with \`sections: ["decisions", "discoveries"]\` if your task touches architecture or prior design choices._

---
EOF
```

Then substitute template variables. The implementer playbook has placeholders like `${NUMBER}`, `${BASE_BRANCH}`, `${VERIFY_COMMAND}`, `${E2E_COMMAND}`, and `${STRATEGIC_CONTEXT_PREAMBLE}`. Read the verify/e2e commands from the project config:

```bash
N=42
BASE=feature/login
VERIFY=$(jq -r '.verify.all // ""' /workspace/.cspace.json 2>/dev/null || echo "")
E2E=$(jq -r '.verify.e2e // ""' /workspace/.cspace.json 2>/dev/null || echo "")

# Resolve playbook path: project override → cspace default
PLAYBOOK=/opt/cspace/lib/agents/implementer.md
[ -f /workspace/.cspace/agents/implementer.md ] && PLAYBOOK=/workspace/.cspace/agents/implementer.md

# Inline the preamble file — avoids sed line-break headaches.
PREAMBLE=$(cat /tmp/preamble-$N.md)

# Use a python one-liner for a multi-line-safe substitution of the preamble.
python3 -c "
import sys, pathlib
p = pathlib.Path('$PLAYBOOK').read_text()
p = p.replace('\${STRATEGIC_CONTEXT_PREAMBLE}', '''$PREAMBLE''')
sys.stdout.write(p)
" | sed \
  -e "s|\${NUMBER}|$N|g" \
  -e "s|\${BASE_BRANCH}|$BASE|g" \
  -e "s|\${VERIFY_COMMAND}|$VERIFY|g" \
  -e "s|\${E2E_COMMAND}|$E2E|g" \
  -e "s|\${MILESTONE_FLAG}||g" \
  > /tmp/implementer-$N.txt
```

If `read_context` is unavailable (tool not registered), substitute `${STRATEGIC_CONTEXT_PREAMBLE}` with an empty string and continue — sub-agents can still call `read_context` themselves at runtime.
````

- [ ] **Step 2: Verify the file still parses as markdown**

Run: `wc -l lib/agents/coordinator.md`
Expected: line count printed (file intact).

- [ ] **Step 3: Commit**

```bash
git add lib/agents/coordinator.md
git commit -m "coordinator: build dispatch preamble from read_context MCP tool"
```

---

## Task 12: Implementer playbook — document context tools

**Files:**
- Modify: `lib/agents/implementer.md`

- [ ] **Step 1: Add a Phase 1.5 "Project Context" block**

Find "## Phase 1 — Setup" (around line 9 in the head we read) and its numbered steps (1–2). Directly after step 2 (`gh pr create --draft ...`), insert a new sub-section:

```markdown
### Project context

If the `cspace-context` MCP server is available (you'll see `mcp__cspace_context__*` tools), use it:

- Direction and roadmap may already be at the top of this prompt under `## Project Context`. If not, call `read_context` with `sections: ["direction", "roadmap"]`.
- Before designing (Phase 3), if the task touches architecture, existing abstractions, or prior design choices, call `read_context` with `sections: ["decisions", "discoveries"]` to avoid re-litigating settled questions.
```

- [ ] **Step 2: Add logging guidance to Phase 6 — Ship**

Find "## Phase 6 — Ship" and its numbered steps. After step 17 (`gh pr edit --body` note), append a new step:

```markdown
17a. **Log what's worth preserving.** If you made a significant design decision, call `log_decision` (title, context, alternatives, decision, consequences). If you learned something non-obvious about the code or infrastructure, call `log_discovery` (title, finding, impact). Only log things that would save a future session time — not every minor implementation choice. Do not log code conventions, commands, or anything already obvious from the diff or git history.
```

- [ ] **Step 3: Commit**

```bash
git add lib/agents/implementer.md
git commit -m "implementer: document read_context / log_decision / log_discovery"
```

---

## Task 13: CLAUDE.md — point humans at docs/context/

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Add a "Project Context" section**

Open `CLAUDE.md`. Below "## Project Overview" (which is near the top), insert a new section:

```markdown
## Project Context

The `docs/context/` directory holds layered planning context accessible via the `cspace-context` MCP server.

- `direction.md`, `principles.md`, `roadmap.md` — human-owned. Edit directly.
- `decisions/` and `discoveries/` — agent-owned. Written by agents via `log_decision` / `log_discovery`. Curate with the `remove_entry` tool or by deleting files.

Agents call `read_context` at the start of non-trivial work. See `docs/superpowers/specs/2026-04-13-context-mcp-server-design.md` for the full contract.
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "CLAUDE.md: document docs/context and cspace-context MCP server"
```

---

## Task 14: Remove `sync-context` command and docs

**Files:**
- Delete: `internal/cli/sync_context.go`
- Modify: `internal/cli/root.go`
- Modify: `docs/src/content/docs/cli-reference/overview.md`
- Modify: `docs/src/content/docs/cli-reference/instance-management.md`

- [ ] **Step 1: Delete the command file**

Run: `git rm internal/cli/sync_context.go`
Expected: file removed from index.

- [ ] **Step 2: Remove registration from root.go**

Edit `internal/cli/root.go`. Find `newSyncContextCmd(),` in the `root.AddCommand(...)` list and delete that line.

- [ ] **Step 3: Remove doc references**

In `docs/src/content/docs/cli-reference/overview.md`, delete the table row that starts with `` | [`cspace sync-context`] ``.

In `docs/src/content/docs/cli-reference/instance-management.md`, delete the entire `## `cspace sync-context`` section. It spans from its `##` heading through the end of its command examples (just before the next `##` heading or end of file). Check line 293 onwards — delete to the start of the next `## ` section (or EOF if it's the last section).

- [ ] **Step 4: Verify build**

Run: `make build && make test && make vet`
Expected: PASS. All references removed, nothing compiles against `sync-context`.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "Remove sync-context command; superseded by cspace-context MCP server"
```

---

## Task 15: Final integration check

**Files:** none modified

- [ ] **Step 1: Clean build and full test suite**

Run: `make clean && make build && make test && make vet`
Expected: all green.

- [ ] **Step 2: Smoke test the binary**

Run: `./bin/cspace-go context-server --help`
Expected: help text prints.

Run: `./bin/cspace-go --help 2>&1 | grep context-server`
Expected: `context-server` appears in the "Other" command group.

- [ ] **Step 3: Manual smoke via MCP client (optional, recommended)**

In a separate scratch directory:

```bash
mkdir /tmp/cspace-context-smoke && cd /tmp/cspace-context-smoke
# Requires `mcp` CLI or a JSON-RPC harness. Skip if not set up.
# Alternatively, point a local Claude session at .mcp.json and call read_context.
```

If not feasible in this session, defer to the human with a note describing what to verify.

- [ ] **Step 4: No commit needed**

This task is verification only.

---

## Self-review checklist (for the plan author, not the implementer)

Before starting execution, double-check:

1. **Spec coverage:**
   - ✅ read_context / log_decision / log_discovery / list_entries / remove_entry — all in Task 7
   - ✅ File format (frontmatter + sections) — Task 2
   - ✅ Slug generation + collisions — Task 1
   - ✅ Seeding on first write — Task 3
   - ✅ Standalone Go subcommand — Tasks 5–6
   - ✅ Coordinator push of direction+roadmap — Task 11
   - ✅ Implementer pull of decisions+discoveries — Task 12
   - ✅ Host wiring (.mcp.json) — Task 9
   - ✅ Container wiring (init-claude-plugins.sh) — Task 10
   - ✅ CLAUDE.md integration — Task 13
   - ✅ E2E test — Task 8

2. **Removed scope:** sync-context cleanup (Task 14) — replaces the old milestone-context flow mentioned in the spec's problem statement.

3. **Type consistency:** `Store`, `Entry`, `Kind`, `ListOptions`, `EntrySummary` — names stable across Tasks 1–4 and reused in Tasks 7–8. DTO names (`entryDTO`, `entrySummaryDTO`) localized to `internal/cli/context_server.go`.
