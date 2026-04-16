package contextstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLogFindingDefaultsStatusOpen(t *testing.T) {
	s := newStore(t)
	path, err := s.LogFinding(LogFindingInput{
		Title:    "Onboarding step X confusion",
		Category: FindingCategoryObservation,
		Summary:  "Users stop at step X",
		Details:  "5/7 personas affected",
	})
	if err != nil {
		t.Fatalf("LogFinding: %v", err)
	}
	want := filepath.Join(s.Root, "docs/context/findings/2026-04-13-onboarding-step-x-confusion.md")
	if path != want {
		t.Errorf("path: got %q, want %q", path, want)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(body)
	for _, want := range []string{
		"kind: finding",
		"status: open",
		"category: observation",
		"## Summary\nUsers stop at step X",
		"## Details\n5/7 personas affected",
		"## Updates\n### 2026-04-13T00:00:00Z",
		"status: open",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("missing %q in:\n%s", want, raw)
		}
	}
}

func TestLogFindingRejectsBadCategory(t *testing.T) {
	s := newStore(t)
	_, err := s.LogFinding(LogFindingInput{
		Title:    "x",
		Category: "feature-request", // not in ValidFindingCategories
	})
	if err == nil {
		t.Fatal("expected error for invalid category")
	}
	if !strings.Contains(err.Error(), "category") {
		t.Errorf("error should mention category, got: %v", err)
	}
}

func TestLogFindingRejectsBadStatus(t *testing.T) {
	s := newStore(t)
	_, err := s.LogFinding(LogFindingInput{
		Title:    "x",
		Category: FindingCategoryBug,
		Status:   "pending", // not in ValidFindingStatuses
	})
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestLogFindingRequiresTitle(t *testing.T) {
	s := newStore(t)
	_, err := s.LogFinding(LogFindingInput{Category: FindingCategoryBug})
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestAppendToFindingPreservesPriorUpdates(t *testing.T) {
	s := newStore(t)
	path, err := s.LogFinding(LogFindingInput{
		Title:    "Recurring 500 on /api/foo",
		Category: FindingCategoryBug,
		Summary:  "500 under load",
	})
	if err != nil {
		t.Fatal(err)
	}
	slug := strings.TrimSuffix(filepath.Base(path), ".md")

	// Advance the clock so each append gets a distinct timestamp.
	s.Now = func() time.Time { return time.Date(2026, 4, 14, 10, 0, 0, 0, time.UTC) }
	if _, _, err := s.AppendToFinding(AppendFindingInput{
		Slug:   slug,
		Note:   "reproduced locally",
		Author: "implementer-1",
	}); err != nil {
		t.Fatalf("first append: %v", err)
	}

	s.Now = func() time.Time { return time.Date(2026, 4, 15, 14, 30, 0, 0, time.UTC) }
	if _, _, err := s.AppendToFinding(AppendFindingInput{
		Slug:   slug,
		Note:   "proposing a retry with backoff",
		Status: FindingStatusAcknowledged,
		Author: "coord",
	}); err != nil {
		t.Fatalf("second append: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)

	// The seed update + both appends must all survive, in order.
	seed := "2026-04-13T00:00:00Z"
	first := "2026-04-14T10:00:00Z"
	second := "2026-04-15T14:30:00Z"
	seedIdx := strings.Index(got, seed)
	firstIdx := strings.Index(got, first)
	secondIdx := strings.Index(got, second)
	if seedIdx < 0 || firstIdx < 0 || secondIdx < 0 {
		t.Fatalf("expected all three timestamps in file:\n%s", got)
	}
	if !(seedIdx < firstIdx && firstIdx < secondIdx) {
		t.Errorf("update order wrong: seed=%d first=%d second=%d", seedIdx, firstIdx, secondIdx)
	}
	// Notes are preserved.
	if !strings.Contains(got, "reproduced locally") {
		t.Error("first note missing")
	}
	if !strings.Contains(got, "proposing a retry with backoff") {
		t.Error("second note missing")
	}
	// Status transitioned.
	if !strings.Contains(got, "\nstatus: acknowledged\n") {
		t.Errorf("frontmatter status did not update to acknowledged:\n%s", got)
	}
}

func TestAppendToFindingUpdatesStatusOnly(t *testing.T) {
	s := newStore(t)
	path, err := s.LogFinding(LogFindingInput{
		Title:    "X",
		Category: FindingCategoryBug,
	})
	if err != nil {
		t.Fatal(err)
	}
	slug := strings.TrimSuffix(filepath.Base(path), ".md")

	_, newStatus, err := s.AppendToFinding(AppendFindingInput{
		Slug:   slug,
		Note:   "shipped in #42",
		Status: FindingStatusResolved,
	})
	if err != nil {
		t.Fatal(err)
	}
	if newStatus != FindingStatusResolved {
		t.Errorf("returned status: got %q, want %q", newStatus, FindingStatusResolved)
	}

	// ReadFinding should reflect the new status.
	e, err := s.ReadFinding(slug)
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != FindingStatusResolved {
		t.Errorf("ReadFinding status: got %q, want %q", e.Status, FindingStatusResolved)
	}
}

func TestAppendToFindingNotFound(t *testing.T) {
	s := newStore(t)
	_, _, err := s.AppendToFinding(AppendFindingInput{
		Slug: "2026-04-13-does-not-exist",
		Note: "n",
	})
	if err == nil {
		t.Fatal("expected error for missing slug")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found', got: %v", err)
	}
}

func TestAppendToFindingRejectsBadStatus(t *testing.T) {
	s := newStore(t)
	path, _ := s.LogFinding(LogFindingInput{
		Title: "x", Category: FindingCategoryBug,
	})
	slug := strings.TrimSuffix(filepath.Base(path), ".md")
	_, _, err := s.AppendToFinding(AppendFindingInput{
		Slug: slug, Note: "n", Status: "in-progress",
	})
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestAppendToFindingRefusesToTouchNonFinding(t *testing.T) {
	s := newStore(t)
	// Create a decision and try to append to it using its slug. Even though
	// the slugs share the namespace visually, AppendToFinding always resolves
	// under docs/context/findings/, so the attempt should fail as "not found."
	dPath, err := s.LogDecision(LogDecisionInput{Title: "X"})
	if err != nil {
		t.Fatal(err)
	}
	slug := strings.TrimSuffix(filepath.Base(dPath), ".md")
	_, _, err = s.AppendToFinding(AppendFindingInput{Slug: slug, Note: "n"})
	if err == nil {
		t.Fatal("expected error when appending to a non-finding slug")
	}
}

func TestAppendToFindingConcurrentWrites(t *testing.T) {
	s := newStore(t)
	path, err := s.LogFinding(LogFindingInput{
		Title:    "Parallel test",
		Category: FindingCategoryBug,
	})
	if err != nil {
		t.Fatal(err)
	}
	slug := strings.TrimSuffix(filepath.Base(path), ".md")

	// Ten goroutines each append a uniquely-tagged note. With flock +
	// atomic rename, all ten tags must end up in the final Updates body.
	// We use a shared counter for Now so each append gets a distinct
	// timestamp (otherwise the subheadings would all look identical).
	var clockMu sync.Mutex
	tick := 0
	s.Now = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		tick++
		return time.Date(2026, 4, 13, 0, 0, tick, 0, time.UTC)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _, err := s.AppendToFinding(AppendFindingInput{
				Slug:   slug,
				Note:   fmt.Sprintf("note-%02d", n),
				Author: fmt.Sprintf("worker-%02d", n),
			})
			if err != nil {
				t.Errorf("append %d: %v", n, err)
			}
		}(i)
	}
	wg.Wait()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for i := 0; i < 10; i++ {
		marker := fmt.Sprintf("note-%02d", i)
		if !strings.Contains(got, marker) {
			t.Errorf("update %q missing from final file; flock didn't serialize writes", marker)
		}
	}
}

func TestListFindingsFilterByStatusAndCategory(t *testing.T) {
	s := newStore(t)
	// Four findings across two categories and two statuses.
	cases := []struct {
		title, category, status string
	}{
		{"bug-open", FindingCategoryBug, FindingStatusOpen},
		{"bug-resolved", FindingCategoryBug, FindingStatusResolved},
		{"refactor-open", FindingCategoryRefactor, FindingStatusOpen},
		{"obs-acked", FindingCategoryObservation, FindingStatusAcknowledged},
	}
	for _, c := range cases {
		if _, err := s.LogFinding(LogFindingInput{
			Title: c.title, Category: c.category, Status: c.status,
		}); err != nil {
			t.Fatalf("LogFinding %s: %v", c.title, err)
		}
	}

	// status=open alone → 2 matches (bug-open, refactor-open)
	got, err := s.ListEntries(ListOptions{
		Kind:   KindFinding,
		Status: []string{FindingStatusOpen},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("status=open: got %d, want 2", len(got))
	}

	// status=open + category=bug → 1 match
	got, err = s.ListEntries(ListOptions{
		Kind:     KindFinding,
		Status:   []string{FindingStatusOpen},
		Category: []string{FindingCategoryBug},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("status=open + category=bug: got %d, want 1", len(got))
	}

	// Multiple statuses → OR semantics
	got, err = s.ListEntries(ListOptions{
		Kind:   KindFinding,
		Status: []string{FindingStatusOpen, FindingStatusAcknowledged},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("status=[open,acknowledged]: got %d, want 3", len(got))
	}
}

func TestListFindingsTagIntersection(t *testing.T) {
	s := newStore(t)
	_, _ = s.LogFinding(LogFindingInput{
		Title: "ux-issue", Category: FindingCategoryBug,
		Tags: []string{"ux", "signup"},
	})
	_, _ = s.LogFinding(LogFindingInput{
		Title: "perf-issue", Category: FindingCategoryBug,
		Tags: []string{"perf", "api"},
	})
	_, _ = s.LogFinding(LogFindingInput{
		Title: "untagged", Category: FindingCategoryBug,
	})

	got, err := s.ListEntries(ListOptions{
		Kind: KindFinding,
		Tags: []string{"ux"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Slug != "2026-04-13-ux-issue" {
		t.Errorf("tags=ux: got %+v, want [ux-issue]", got)
	}

	// Multi-tag filter: any intersection counts
	got, err = s.ListEntries(ListOptions{
		Kind: KindFinding,
		Tags: []string{"perf", "nonexistent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Slug != "2026-04-13-perf-issue" {
		t.Errorf("tags=[perf,nonexistent]: got %+v", got)
	}
}

func TestReadFindingMissing(t *testing.T) {
	s := newStore(t)
	_, err := s.ReadFinding("2026-04-13-does-not-exist")
	if err == nil {
		t.Fatal("expected error for missing finding")
	}
}

func TestReadFindingRejectsInvalidSlug(t *testing.T) {
	s := newStore(t)
	_, err := s.ReadFinding("../escape")
	if err == nil {
		t.Fatal("expected error for path-traversal slug")
	}
}
