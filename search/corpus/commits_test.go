package corpus

import (
	"strings"
	"testing"
)

// TestCommitCorpus_EnumerateEmitsCommitRecords verifies the mechanics of
// Enumerate against the current git repo: shape-only, no assertion on
// specific commit subjects. An earlier version of this test asserted that
// a particular "known commit" was in the latest 50 — that kept breaking
// as work landed on top of it, so we swapped to the sturdier contract:
// Enumerate emits Records that are well-formed and kind=commit.
func TestCommitCorpus_EnumerateEmitsCommitRecords(t *testing.T) {
	cc := &CommitCorpus{Limit: 10}
	records, errs := cc.Enumerate(".")

	// Drain errors in the background.
	go func() {
		for range errs {
		}
	}()

	var count int
	for rec := range records {
		count++
		if rec.Kind != "commit" {
			t.Errorf("expected Kind=commit, got %q", rec.Kind)
		}
		if rec.Path == "" {
			t.Errorf("record missing Path (commit hash): %+v", rec)
		}
		if rec.EmbedText == "" {
			t.Errorf("record missing EmbedText: %+v", rec)
		}
	}
	if count == 0 {
		t.Error("expected at least one commit record from Enumerate, got none")
	}
}

func TestCommitCorpus_CollectionName(t *testing.T) {
	cc := &CommitCorpus{}
	got := cc.Collection(".")
	if !strings.HasPrefix(got, "commits-") {
		t.Errorf("expected collection to start with commits-, got %q", got)
	}
}
