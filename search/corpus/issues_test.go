package corpus

import (
	"fmt"
	"strings"
	"testing"
)

// --- parseOwnerRepo tests ---

func TestParseOwnerRepo_HTTPS(t *testing.T) {
	owner, repo, err := parseOwnerRepo("https://github.com/foo/bar.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owner != "foo" || repo != "bar" {
		t.Errorf("got (%q, %q), want (foo, bar)", owner, repo)
	}
}

func TestParseOwnerRepo_HTTPSNoGitSuffix(t *testing.T) {
	owner, repo, err := parseOwnerRepo("https://github.com/foo/bar")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owner != "foo" || repo != "bar" {
		t.Errorf("got (%q, %q), want (foo, bar)", owner, repo)
	}
}

func TestParseOwnerRepo_SSH(t *testing.T) {
	owner, repo, err := parseOwnerRepo("git@github.com:foo/bar.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owner != "foo" || repo != "bar" {
		t.Errorf("got (%q, %q), want (foo, bar)", owner, repo)
	}
}

func TestParseOwnerRepo_SSHNoGitSuffix(t *testing.T) {
	owner, repo, err := parseOwnerRepo("git@github.com:foo/bar")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owner != "foo" || repo != "bar" {
		t.Errorf("got (%q, %q), want (foo, bar)", owner, repo)
	}
}

func TestParseOwnerRepo_Invalid(t *testing.T) {
	_, _, err := parseOwnerRepo("not-a-url")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

// --- IssuesCorpus basic tests ---

func TestIssuesCorpus_ID(t *testing.T) {
	c := &IssuesCorpus{}
	if got := c.ID(); got != "issues" {
		t.Errorf("ID() = %q, want %q", got, "issues")
	}
}

func TestIssuesCorpus_Collection(t *testing.T) {
	c := &IssuesCorpus{}
	got := c.Collection(".")
	if !strings.HasPrefix(got, "issues-") {
		t.Errorf("Collection(.) = %q, want prefix %q", got, "issues-")
	}
}

// --- Fake ghFetcher for Enumerate tests ---

type fakeGHFetcher struct {
	issues   []ghIssue
	comments map[int][]ghComment
}

func (f *fakeGHFetcher) ListIssues(owner, repo string, limit int) ([]ghIssue, error) {
	if limit > 0 && limit < len(f.issues) {
		return f.issues[:limit], nil
	}
	return f.issues, nil
}

func (f *fakeGHFetcher) ListComments(owner, repo string, number int) ([]ghComment, error) {
	if c, ok := f.comments[number]; ok {
		return c, nil
	}
	return nil, nil
}

// --- Enumerate tests ---

func TestIssuesCorpus_Enumerate_ThreeIssues(t *testing.T) {
	fake := &fakeGHFetcher{
		issues: []ghIssue{
			{
				Number:    1,
				Title:     "Bug: crash on startup",
				Body:      "The app crashes when you open it.",
				State:     "open",
				Author:    "alice",
				Labels:    []string{"bug"},
				CreatedAt: "2026-01-01T00:00:00Z",
				UpdatedAt: "2026-01-02T00:00:00Z",
				HTMLURL:   "https://github.com/foo/bar/issues/1",
				PRURL:     "",
			},
			{
				Number:    2,
				Title:     "Add dark mode",
				Body:      "Please add dark mode support.",
				State:     "closed",
				Author:    "bob",
				Labels:    []string{"enhancement", "ui"},
				CreatedAt: "2026-02-01T00:00:00Z",
				UpdatedAt: "2026-02-15T00:00:00Z",
				HTMLURL:   "https://github.com/foo/bar/issues/2",
				PRURL:     "",
			},
			{
				Number:    3,
				Title:     "Fix dark mode contrast",
				Body:      "The contrast ratio is too low.",
				State:     "open",
				Author:    "charlie",
				Labels:    nil,
				CreatedAt: "2026-03-01T00:00:00Z",
				UpdatedAt: "2026-03-02T00:00:00Z",
				HTMLURL:   "https://github.com/foo/bar/pull/3",
				PRURL:     "https://github.com/foo/bar/pull/3",
			},
		},
		comments: map[int][]ghComment{
			2: {
				{Author: "alice", CreatedAt: "2026-02-02T00:00:00Z", Body: "Good idea, +1"},
				{Author: "bob", CreatedAt: "2026-02-03T00:00:00Z", Body: "Thanks!"},
			},
		},
	}

	c := &IssuesCorpus{Limit: 10, fetcher: fake, remoteURL: "https://github.com/foo/bar.git"}
	records, errs := c.Enumerate(".")

	go func() {
		for range errs {
		}
	}()

	var recs []Record
	for rec := range records {
		recs = append(recs, rec)
	}

	if len(recs) != 3 {
		t.Fatalf("expected 3 records, got %d", len(recs))
	}

	// Issue #1: plain issue, no comments
	r1 := recs[0]
	if r1.Path != "issue#1" {
		t.Errorf("rec[0].Path = %q, want %q", r1.Path, "issue#1")
	}
	if r1.Kind != "issue" {
		t.Errorf("rec[0].Kind = %q, want %q", r1.Kind, "issue")
	}
	if !strings.Contains(r1.EmbedText, "Issue #1: Bug: crash on startup") {
		t.Errorf("rec[0].EmbedText missing header, got:\n%s", r1.EmbedText)
	}
	if r1.Extra["is_pr"] != false {
		t.Errorf("rec[0].Extra[is_pr] = %v, want false", r1.Extra["is_pr"])
	}

	// Issue #2: closed issue with comments
	r2 := recs[1]
	if r2.Path != "issue#2" {
		t.Errorf("rec[1].Path = %q, want %q", r2.Path, "issue#2")
	}
	if r2.Extra["state"] != "closed" {
		t.Errorf("rec[1].Extra[state] = %v, want %q", r2.Extra["state"], "closed")
	}
	if !strings.Contains(r2.EmbedText, "--- Comment by alice") {
		t.Errorf("rec[1].EmbedText missing comment by alice, got:\n%s", r2.EmbedText)
	}
	if !strings.Contains(r2.EmbedText, "Good idea, +1") {
		t.Errorf("rec[1].EmbedText missing comment body, got:\n%s", r2.EmbedText)
	}
	labels, ok := r2.Extra["labels"].([]string)
	if !ok || len(labels) != 2 || labels[0] != "enhancement" {
		t.Errorf("rec[1].Extra[labels] = %v, want [enhancement ui]", r2.Extra["labels"])
	}

	// Issue #3: PR (has PRURL set)
	r3 := recs[2]
	if r3.Path != "issue#3" {
		t.Errorf("rec[2].Path = %q, want %q", r3.Path, "issue#3")
	}
	if r3.Extra["is_pr"] != true {
		t.Errorf("rec[2].Extra[is_pr] = %v, want true", r3.Extra["is_pr"])
	}
}

func TestIssuesCorpus_Enumerate_NoBodyNoComments(t *testing.T) {
	fake := &fakeGHFetcher{
		issues: []ghIssue{
			{
				Number:    42,
				Title:     "Empty issue",
				Body:      "",
				State:     "open",
				Author:    "dev",
				Labels:    nil,
				CreatedAt: "2026-04-01T00:00:00Z",
				UpdatedAt: "2026-04-01T00:00:00Z",
				HTMLURL:   "https://github.com/foo/bar/issues/42",
				PRURL:     "",
			},
		},
		comments: map[int][]ghComment{},
	}

	c := &IssuesCorpus{Limit: 10, fetcher: fake, remoteURL: "https://github.com/foo/bar.git"}
	records, errs := c.Enumerate(".")

	go func() {
		for range errs {
		}
	}()

	var recs []Record
	for rec := range records {
		recs = append(recs, rec)
	}

	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}

	r := recs[0]
	if r.EmbedText == "" {
		t.Error("expected non-empty EmbedText for issue with no body and no comments")
	}
	if !strings.Contains(r.EmbedText, "Issue #42: Empty issue") {
		t.Errorf("EmbedText missing header, got:\n%s", r.EmbedText)
	}
	if !strings.Contains(r.EmbedText, "State: open") {
		t.Errorf("EmbedText missing state, got:\n%s", r.EmbedText)
	}
}

// --- formatIssueEmbedText tests ---

func TestFormatIssueEmbedText_Truncation(t *testing.T) {
	// Create an issue with a very long body to test truncation.
	longBody := strings.Repeat("x", 20000)
	text := formatIssueEmbedText(
		ghIssue{
			Number: 1, Title: "test", Body: longBody,
			State: "open", Author: "a", Labels: nil,
			CreatedAt: "2026-01-01T00:00:00Z",
		},
		nil,
	)
	if len(text) > maxEmbedChars {
		t.Errorf("EmbedText length %d exceeds maxEmbedChars %d", len(text), maxEmbedChars)
	}
}

func TestFormatIssueEmbedText_WithLabels(t *testing.T) {
	text := formatIssueEmbedText(
		ghIssue{
			Number: 5, Title: "feature", Body: "details",
			State: "open", Author: "dev", Labels: []string{"bug", "p1"},
			CreatedAt: "2026-01-01T00:00:00Z",
		},
		nil,
	)
	if !strings.Contains(text, "Labels: bug, p1") {
		t.Errorf("expected labels in embed text, got:\n%s", text)
	}
}

func TestFormatIssueEmbedText_ContentHash(t *testing.T) {
	issue := ghIssue{
		Number: 1, Title: "test", Body: "body",
		State: "open", Author: "a", Labels: nil,
		CreatedAt: "2026-01-01T00:00:00Z",
	}
	text1 := formatIssueEmbedText(issue, nil)
	text2 := formatIssueEmbedText(issue, nil)
	// Same input produces same output (deterministic).
	if text1 != text2 {
		t.Error("formatIssueEmbedText is not deterministic")
	}
	// Different input produces different output.
	issue.Title = "different"
	text3 := formatIssueEmbedText(issue, nil)
	if text1 == text3 {
		t.Error("different titles should produce different embed text")
	}
}

func TestFormatIssueEmbedText_Comments(t *testing.T) {
	text := formatIssueEmbedText(
		ghIssue{
			Number: 10, Title: "with comments", Body: "main body",
			State: "closed", Author: "dev", Labels: nil,
			CreatedAt: "2026-01-01T00:00:00Z",
		},
		[]ghComment{
			{Author: "reviewer", CreatedAt: "2026-01-02T00:00:00Z", Body: "Looks good"},
		},
	)
	if !strings.Contains(text, "--- Comment by reviewer") {
		t.Errorf("missing comment header in embed text:\n%s", text)
	}
	if !strings.Contains(text, "Looks good") {
		t.Errorf("missing comment body in embed text:\n%s", text)
	}
}

// --- Record shape validation ---

func TestIssuesCorpus_RecordContentHash(t *testing.T) {
	fake := &fakeGHFetcher{
		issues: []ghIssue{
			{
				Number: 1, Title: "test", Body: "body",
				State: "open", Author: "a", Labels: nil,
				CreatedAt: "2026-01-01T00:00:00Z",
				UpdatedAt: "2026-01-01T00:00:00Z",
				HTMLURL:   "https://github.com/foo/bar/issues/1",
				PRURL:     "",
			},
		},
		comments: map[int][]ghComment{},
	}

	c := &IssuesCorpus{Limit: 10, fetcher: fake, remoteURL: "https://github.com/foo/bar.git"}
	records, errs := c.Enumerate(".")

	go func() {
		for range errs {
		}
	}()

	rec := <-records
	if rec.ContentHash == "" {
		t.Error("expected non-empty ContentHash")
	}
	// ContentHash should be a hex sha256 (64 chars).
	if len(rec.ContentHash) != 64 {
		t.Errorf("ContentHash length = %d, want 64 hex chars", len(rec.ContentHash))
	}

	// Verify Extra fields.
	if rec.Extra["number"] != 1 {
		t.Errorf("Extra[number] = %v, want 1", rec.Extra["number"])
	}
	if rec.Extra["title"] != "test" {
		t.Errorf("Extra[title] = %v, want %q", rec.Extra["title"], "test")
	}
	if rec.Extra["url"] != "https://github.com/foo/bar/issues/1" {
		t.Errorf("Extra[url] = %v, want the html url", rec.Extra["url"])
	}
	if rec.Extra["author"] != "a" {
		t.Errorf("Extra[author] = %v, want %q", rec.Extra["author"], "a")
	}

	fmt.Println("ContentHash:", rec.ContentHash)
}
