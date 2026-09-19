package controlplane

import (
	"errors"
	"strings"
	"testing"
)

func TestInitRunsTheStartupSweep(t *testing.T) {
	h := &fakeHost{t: t}
	m := New(&fakeData{snap: testSnapshot()}, &recordingActor{}, h, nopClipboard{}, NewKeyMap(nil))
	for _, msg := range drain(m.Init()) {
		if _, ok := msg.(sweepMsg); ok {
			if h.sweeps != 1 {
				t.Errorf("sweeps = %d, want 1", h.sweeps)
			}
			return
		}
	}
	t.Fatal("Init never swept")
}

// runSweep drives a fresh Model through Init with a host configured to
// return outcome/sweepErr from Sweep, feeds the resulting sweepMsg through
// Update, and returns the model afterwards — exercising the whole path from
// sweepCmd through to the footer notice, not just Update's sweepMsg case in
// isolation.
func runSweep(t *testing.T, outcome SweepOutcome, sweepErr error) Model {
	t.Helper()
	h := &fakeHost{t: t, sweep: outcome, sweepErr: sweepErr}
	m := New(&fakeData{snap: testSnapshot()}, &recordingActor{}, h, nopClipboard{}, NewKeyMap(nil))
	for _, msg := range drain(m.Init()) {
		if _, ok := msg.(sweepMsg); ok {
			mm, _ := m.Update(msg)
			return mm.(Model)
		}
	}
	t.Fatal("Init never swept")
	return Model{}
}

// TestSweepNoticeNamesEachNonZeroCount pins the notice text a successful
// sweep with something to report produces: every nonzero count named, in
// Detached/Deleted/Errors order.
func TestSweepNoticeNamesEachNonZeroCount(t *testing.T) {
	m := runSweep(t, SweepOutcome{Detached: 1, Deleted: 2, Errors: 1}, nil)
	want := "startup sweep: detached 1, deleted 2, 1 error"
	if m.notice.text != want {
		t.Errorf("notice = %q, want %q", m.notice.text, want)
	}
	if m.notice.isErr {
		t.Error("a successful sweep's outcome notice must not be sticky")
	}
}

// TestSweepNoticePluralizesMultipleErrors — "error" is a noun here, unlike
// "detached"/"deleted", so it is the one word in the notice that has to
// agree with its count.
func TestSweepNoticePluralizesMultipleErrors(t *testing.T) {
	m := runSweep(t, SweepOutcome{Errors: 2}, nil)
	want := "startup sweep: 2 errors"
	if m.notice.text != want {
		t.Errorf("notice = %q, want %q", m.notice.text, want)
	}
}

// TestSweepNoticeStaysQuietWhenNothingHappened — a sweep that detached
// nothing, deleted nothing and errored on nothing is not news.
func TestSweepNoticeStaysQuietWhenNothingHappened(t *testing.T) {
	m := runSweep(t, SweepOutcome{}, nil)
	if m.notice.text != "" {
		t.Errorf("notice = %q, want none for a sweep that found nothing", m.notice.text)
	}
}

// TestSweepFailureIsAStickyErrorNotice — a sweep that could not even run
// (SweepClientRecords itself returning an error, e.g. ErrNoHome) is a
// different kind of bad news than an outcome with Errors > 0, and gets the
// sticky isErr treatment the outcome notice does not.
func TestSweepFailureIsAStickyErrorNotice(t *testing.T) {
	m := runSweep(t, SweepOutcome{}, errors.New("boom"))
	if !m.notice.isErr {
		t.Error("a failed sweep must produce a sticky error notice")
	}
	if !strings.Contains(m.notice.text, "boom") {
		t.Errorf("notice = %q, want it to include the sweep's own error", m.notice.text)
	}
}
