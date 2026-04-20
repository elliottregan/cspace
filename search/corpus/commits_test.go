package corpus

import (
	"strings"
	"testing"
)

// Test that CommitCorpus.Enumerate on the cspace repo emits at least one
// known commit with the expected subject. Uses the current working tree.
func TestCommitCorpus_EnumerateIncludesKnownCommit(t *testing.T) {
	cc := &CommitCorpus{Limit: 50}
	records, errs := cc.Enumerate(".")

	// Drain errors in the background.
	go func() {
		for range errs {
		}
	}()

	found := false
	for rec := range records {
		if rec.Kind != "commit" {
			t.Errorf("expected Kind=commit, got %q", rec.Kind)
		}
		if strings.Contains(rec.EmbedText, "semantic git search") {
			found = true
		}
	}
	if !found {
		t.Error("expected to find a commit mentioning 'semantic git search' in the recent history")
	}
}

func TestCommitCorpus_CollectionName(t *testing.T) {
	cc := &CommitCorpus{}
	got := cc.Collection(".")
	if !strings.HasPrefix(got, "commits-") {
		t.Errorf("expected collection to start with commits-, got %q", got)
	}
}
