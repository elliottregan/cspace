package controlplane

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/elliottregan/cspace/internal/control"
)

func headerURLSet(plan headerPlan) map[string]bool {
	urls := map[string]bool{}
	for _, link := range plan.links {
		if link.url != "" {
			urls[link.url] = true
		}
	}
	return urls
}

func TestHeaderShowsFavoriteServicesAndKeepsOtherServicesInDetails(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	k := keyOf(m.selectedRow())
	m.headers[k] = headerSample{status: control.HeaderStatus{Branch: "feature/header", Dirty: true, PR: &control.PullRequestStatus{Number: 42, URL: "https://github.com/owner/repo/pull/42", CheckStatus: "running"}}}
	m.services[k] = []control.ServiceStatus{
		{Port: 3000, Label: " dev ", URL: "http://mercury.alpha.cspace.test:3000/", State: control.ServiceRunning, Declared: true},
		{Port: 3210, Label: "convex", URL: "http://mercury.alpha.cspace.test:3210/", State: control.ServiceRunning, Declared: true},
		{Port: 4173, Label: "PREVIEW", URL: "http://mercury.alpha.cspace.test:4173/", State: control.ServiceRunning, Declared: true},
		{Port: 5173, URL: "http://mercury.alpha.cspace.test:5173/", State: control.ServiceRunning},
	}
	plan := m.planHeader(100)
	text := plain(plan.text)
	for _, want := range []string{"☿ mercury", "[Details]", "feature/header *", "PR #42", "● dev", "● preview"} {
		if !strings.Contains(text, want) {
			t.Errorf("header is missing %q: %q", want, text)
		}
	}
	if strings.Contains(text, "convex") || strings.Contains(text, "5173") {
		t.Fatalf("nonfavorite services leaked into the header: %q", text)
	}
	urls := headerURLSet(plan)
	if len(urls) != 3 || !urls[m.headers[k].status.PR.URL] || !urls[m.services[k][0].URL] || !urls[m.services[k][2].URL] {
		t.Fatalf("header URLs = %+v; want only PR, dev, preview", urls)
	}
	m.dialog = &detailsDialog{row: m.selectedRow()}
	var detailText []string
	for _, line := range m.detailsLines(100) {
		detailText = append(detailText, line.text)
	}
	if text := strings.Join(detailText, "\n"); !strings.Contains(text, "convex :3210") || !strings.Contains(text, "service :5173") {
		t.Fatalf("details lost nonfavorite services: %q", text)
	}
}

func TestHeaderStoppedAndUnknownServicesAreVisibleWithoutLinks(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	k := keyOf(m.selectedRow())
	m.services[k] = []control.ServiceStatus{
		{Port: 3000, Label: "dev", URL: "http://mercury.alpha.cspace.test:3000/", State: control.ServiceStopped, Declared: true},
		{Port: 4173, Label: "preview", URL: "http://mercury.alpha.cspace.test:4173/", State: control.ServiceUnknown, Declared: true},
	}
	plan := m.planHeader(80)
	if text := plain(plan.text); !strings.Contains(text, "○ dev") || !strings.Contains(text, "? preview") {
		t.Fatalf("service states are not distinguishable: %q", text)
	}
	if urls := headerURLSet(plan); len(urls) != 0 {
		t.Fatalf("stopped/unknown services are clickable: %+v", urls)
	}
}

func TestHeaderUsesActivePaneRatherThanSidebarSelection(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	mercury := sandboxKey{Project: "alpha", Name: "mercury"}
	other := sandboxKey{Project: "alpha", Name: "issue-42"}
	m.headers[mercury] = headerSample{status: control.HeaderStatus{Branch: "active-branch", PR: &control.PullRequestStatus{Number: 11, URL: "https://github.com/owner/repo/pull/11"}}}
	m.headers[other] = headerSample{status: control.HeaderStatus{Branch: "selected-branch", PR: &control.PullRequestStatus{Number: 22, URL: "https://github.com/owner/repo/pull/22"}}}
	m.services[mercury] = []control.ServiceStatus{{Port: 3000, Label: "dev", URL: "http://mercury.alpha.cspace.test:3000/", State: control.ServiceRunning}}
	m.services[other] = []control.ServiceStatus{{Port: 4173, Label: "preview", URL: "http://issue-42.alpha.cspace.test:4173/", State: control.ServiceRunning}}
	session := control.Session{Name: "cspace-claude-2", ID: "session-2", State: control.InteractiveState{State: "needs-input"}}
	m.sessions[mercury] = []control.Session{session}
	m.tabs = []*tab{{id: 1, kind: KindClaude, project: "alpha", sandbox: "mercury", session: session}}
	m.focused, m.focus, m.selected = 0, focusSidebar, 3
	plan := m.planHeader(100)
	text := plain(plan.text)
	for _, want := range []string{"☿ mercury", "Claude 2", "needs-input", "active-branch", "PR #11", "● dev"} {
		if !strings.Contains(text, want) {
			t.Errorf("active pane header is missing %q: %q", want, text)
		}
	}
	for _, unwanted := range []string{"issue-42", "selected-branch", "PR #22", "preview"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("sidebar selection contaminated pane header with %q: %q", unwanted, text)
		}
	}
	urls := headerURLSet(plan)
	if urls[m.headers[other].status.PR.URL] || urls[m.services[other][0].URL] {
		t.Fatal("header links target the selected container instead of the active pane")
	}
}

func TestHeaderDoesNotBorrowAnotherSessionsActivity(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	k := keyOf(m.selectedRow())
	m.live[k] = liveState{Interactive: control.InteractiveState{State: "working"}}
	m.tabs = []*tab{{id: 1, kind: KindClaude, project: "alpha", sandbox: "mercury", session: control.Session{Name: "cspace-claude-2", ID: "second"}}}
	m.focused = 0
	if text := plain(m.planHeader(80).text); strings.Contains(text, "working") {
		t.Fatalf("named session borrowed default Claude's state: %q", text)
	}
	m.sessions[k] = []control.Session{{Name: "cspace-claude-2", ID: "second", State: control.InteractiveState{State: "idle"}}}
	if text := plain(m.planHeader(80).text); !strings.Contains(text, "idle") || strings.Contains(text, "working") {
		t.Fatalf("named session did not use its own state: %q", text)
	}
}

func TestHostShellHeaderDoesNotBorrowContainerMetadata(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	k := keyOf(m.selectedRow())
	m.headers[k] = headerSample{status: control.HeaderStatus{Branch: "container-branch", PR: &control.PullRequestStatus{Number: 12, URL: "https://github.com/owner/repo/pull/12"}}}
	m.services[k] = []control.ServiceStatus{{Port: 3000, Label: "dev", URL: "http://mercury.alpha.cspace.test:3000/", State: control.ServiceRunning}}
	m.tabs = []*tab{{id: 1, kind: KindHostShell}}
	m.focused = 0
	plan := m.planHeader(100)
	text := plain(plan.text)
	if strings.Contains(text, "mercury") || strings.Contains(text, "container-branch") || len(headerURLSet(plan)) != 0 {
		t.Fatalf("host shell inherited selected container metadata: %q", text)
	}
}

func TestHeaderStalePRRemainsVisibleAndGitFailureIsExplicit(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	k := keyOf(m.selectedRow())
	m.headers[k] = headerSample{status: control.HeaderStatus{Branch: "feature", PRStale: true, PR: &control.PullRequestStatus{Number: 42, URL: "https://github.com/owner/repo/pull/42", CheckStatus: "success"}}, err: errors.New("network unavailable")}
	plan := m.planHeader(80)
	if text := plain(plan.text); !strings.Contains(text, "PR #42 ~") || !headerURLSet(plan)[m.headers[k].status.PR.URL] {
		t.Fatalf("stale PR disappeared or lost its stale marker: %q", text)
	}
	m.headers[k] = headerSample{err: errors.New("clone missing")}
	if text := plain(m.planHeader(80).text); !strings.Contains(text, "git unavailable") || strings.Contains(text, "PR #") {
		t.Fatalf("git failure was rendered as a valid empty state: %q", text)
	}
}

func TestHeaderReservesSpaceForLinksBeforeLongBranch(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	k := keyOf(m.selectedRow())
	m.headers[k] = headerSample{status: control.HeaderStatus{Branch: strings.Repeat("long-feature/", 20), Dirty: true, PR: &control.PullRequestStatus{Number: 42, URL: "https://github.com/owner/repo/pull/42"}}}
	m.services[k] = []control.ServiceStatus{{Label: "dev", URL: "http://dev/", State: control.ServiceRunning}, {Label: "preview", URL: "http://preview/", State: control.ServiceRunning}}
	for _, width := range []int{32, 50, 100} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			plan := m.planHeader(width)
			if len(headerURLSet(plan)) != 3 {
				t.Fatalf("long branch pushed links out at width %d: %q", width, plain(plan.text))
			}
			lines := strings.Split(plan.text, "\n")
			if len(lines) != 2 {
				t.Fatalf("header uses %d lines instead of two", len(lines))
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > width {
					t.Fatalf("header exceeds width %d: %q", width, plain(line))
				}
			}
			for _, link := range plan.links {
				if link.x < 0 || link.x+link.width > width || link.y < 0 || link.y > 1 {
					t.Fatalf("header hit target exceeds rendered extent: %+v", link)
				}
			}
		})
	}
}
