package controlplane

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/elliottregan/cspace/internal/control"
)

func TestFormatMemory(t *testing.T) {
	cases := map[int64]string{
		16 << 30:  "16G",
		1 << 30:   "1G",
		512 << 20: "512M",
		0:         "-",
	}
	for in, want := range cases {
		if got := formatMemory(in); got != want {
			t.Errorf("formatMemory(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatMemUsage(t *testing.T) {
	const gib = int64(1) << 30
	cases := []struct {
		name      string
		used, cap int64
		want      string
	}{
		// Whole gigabytes would collapse these two distinct sandboxes onto
		// the same "1G", which is why usage keeps one decimal.
		{"gib scale keeps one decimal", 1193979904, 16 * gib, "1.1G/16G"},
		{"gib scale distinguishes neighbours", 1717986918, 16 * gib, "1.6G/16G"},
		{"sub-gib usage renders as MiB", 512 * (1 << 20), 4 * gib, "512M/4G"},
		{"missing sample falls back to cap", 0, 4 * gib, "4G"},
		{"missing sample and no cap", 0, 0, "-"},
		{"usage with no cap shows usage alone", 2 * gib, 0, "2.0G"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatMemUsage(tc.used, tc.cap); got != tc.want {
				t.Errorf("formatMemUsage(%d, %d) = %q, want %q", tc.used, tc.cap, got, tc.want)
			}
		})
	}
}

func TestFormatUptime(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, ""},
		{45 * time.Second, "↑45s"},
		{5 * time.Minute, "↑5m"},
		{2*time.Hour + 14*time.Minute, "↑2h14m"},
	}
	for _, tc := range cases {
		if got := formatUptime(tc.in); got != tc.want {
			t.Errorf("formatUptime(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// formatAge feeds the footer's staleness marker when a poll fails. It is
// Task 5 that renders it; it is tested here with the other formatters.
func TestFormatAge(t *testing.T) {
	now := time.Date(2026, 9, 18, 14, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{"never polled", time.Time{}, "never"},
		{"seconds", now.Add(-12 * time.Second), "12s ago"},
		{"minutes", now.Add(-3 * time.Minute), "3m ago"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatAge(tc.in, now); got != tc.want {
				t.Errorf("formatAge = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderDetailSandbox(t *testing.T) {
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury",
		Container: "cspace-alpha-mercury", State: control.StateRunning,
		IP: "192.168.64.5", MemoryB: 16 << 30, Uptime: 2*time.Hour + 14*time.Minute,
		Agent: control.AgentStatus{Reachable: true, State: "idle", Session: "primary"}}
	live := liveState{
		Agent: control.AgentStatus{Reachable: true, State: "working", Session: "primary",
			QueueDepth: 2, LastEventType: "assistant", LastEventSubtype: "text",
			LastEventTs: "2026-09-18T14:02:11Z"},
		Interactive: control.InteractiveState{State: "needs-input",
			Event: "PreToolUse", At: "2026-09-18T14:03:00Z"},
	}
	ports := []control.Port{{Port: 5173, Label: "web", URL: "http://mercury.alpha.cspace.test:5173/"}}
	events := []control.EventLine{
		{Ts: "2026-09-18T14:02:11Z", Kind: "sdk-event", Type: "assistant", Subtype: "text"},
	}

	out := plain(renderDetail(row, live, ports, nil, events, nil, 1717986918, 70))
	for _, want := range []string{
		"mercury", "running", "↑2h14m", "1.6G/16G",
		"agent: working", "session primary", "queue 2", "assistant/text",
		"claude: needs-input", "PreToolUse",
		"5173", "web", "http://mercury.alpha.cspace.test:5173/",
		"14:02:11", "sdk-event", "assistant/text",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("detail missing %q; got:\n%s", want, out)
		}
	}
}

// An unreachable supervisor has to say so, because send and interrupt are
// off for that row and a person needs to know why.
func TestRenderDetailUnreachableAgent(t *testing.T) {
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury",
		State: control.StateDegraded}
	out := plain(renderDetail(row, liveState{}, nil, nil, nil, nil, 0, 70))
	if !strings.Contains(out, "supervisor unreachable") {
		t.Errorf("detail should name the unreachable supervisor; got:\n%s", out)
	}
	if !strings.Contains(out, "send and interrupt are off") {
		t.Errorf("detail should explain the disabled actions; got:\n%s", out)
	}
}

func TestRenderDetailStoppedSandboxOffersBoot(t *testing.T) {
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "issue-42",
		State: control.StateStopped}
	out := plain(renderDetail(row, liveState{}, nil, nil, nil, nil, 0, 70))
	if !strings.Contains(out, "stopped") || !strings.Contains(out, "press u to boot") {
		t.Errorf("a stopped sandbox should offer the boot key; got:\n%s", out)
	}
}

func TestRenderDetailBrowserHealth(t *testing.T) {
	row := control.Row{Kind: control.RowBrowser, Project: "alpha", Name: "browser (shared)",
		State: control.StateRunning, Browser: control.BrowserHealth{Reachable: true, Version: "Chrome/140"}}
	out := plain(renderDetail(row, liveState{}, nil, nil, nil, nil, 0, 70))
	if !strings.Contains(out, "CDP") || !strings.Contains(out, "Chrome/140") {
		t.Errorf("browser detail should show CDP health; got:\n%s", out)
	}

	row.Browser = control.BrowserHealth{}
	out = plain(renderDetail(row, liveState{}, nil, nil, nil, nil, 0, 70))
	if !strings.Contains(strings.ToLower(out), "restart") {
		t.Errorf("an unreachable browser should hint the restart key; got:\n%s", out)
	}
}

// A sidecar that is not running has no address. The segment is dropped, the
// way the sandbox and browser branches drop theirs, rather than printed as an
// empty field between two separators.
func TestRenderDetailSidecarWithoutAnIP(t *testing.T) {
	row := control.Row{Kind: control.RowSidecar, Project: "alpha", Name: "mercury-convex",
		Container: "cspace-alpha-mercury-convex", State: control.StateStopped, MemoryB: 2 << 30}
	out := plain(renderDetail(row, liveState{}, nil, nil, nil, nil, 0, 70))
	if strings.Contains(out, "· ·") {
		t.Errorf("an empty IP left a double separator behind; got:\n%s", out)
	}
	for _, want := range []string{"cspace-alpha-mercury-convex", "2G", "stopped"} {
		if !strings.Contains(out, want) {
			t.Errorf("the band should still show %q; got:\n%s", want, out)
		}
	}

	// With an address, it is still there.
	row.State, row.IP = control.StateRunning, "192.168.64.7"
	out = plain(renderDetail(row, liveState{}, nil, nil, nil, nil, 0, 70))
	if !strings.Contains(out, "192.168.64.7") {
		t.Errorf("a running sidecar should still show its IP; got:\n%s", out)
	}
}

// A ports probe that failed degrades that one line — the band keeps
// rendering everything else it knows.
func TestRenderDetailPortsError(t *testing.T) {
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury",
		State: control.StateRunning, Agent: control.AgentStatus{Reachable: true, State: "idle"}}
	out := plain(renderDetail(row, liveState{}, nil, errors.New("ss: exit 127"), nil, nil, 0, 70))
	if !strings.Contains(out, "ports unavailable") || !strings.Contains(out, "exit 127") {
		t.Errorf("a failed ports probe should degrade one line; got:\n%s", out)
	}
	if !strings.Contains(out, "mercury") {
		t.Errorf("the rest of the band must still render; got:\n%s", out)
	}

	// ...and at the width the band actually has. Since Task 3 moved it
	// under the sidebar, sidebarInner (23) is its only production width —
	// 70 is a width this line is never rendered at, and the fold is what
	// breaks first. The error text itself is allowed to be cut; the label
	// that says the line degraded is not.
	out = plain(renderDetail(row, liveState{}, nil, errors.New("ss: exit 127"), nil, nil, 0, sidebarInner))
	if !strings.Contains(out, "ports unavailable") {
		t.Errorf("the degraded label did not survive the 23-column fold; got:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w > sidebarInner {
			t.Errorf("line %q is %d cells, wider than the sidebar's %d", line, w, sidebarInner)
		}
	}
}

// Spec, Error handling: every poll failure degrades the fields it feeds. An
// unreadable event log is one of them — "no agent events yet" for a sandbox
// whose log could not be opened is a lie the band must not tell.
func TestRenderDetailEventsError(t *testing.T) {
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury",
		State: control.StateRunning, Agent: control.AgentStatus{Reachable: true, State: "idle"}}
	out := plain(renderDetail(row, liveState{}, nil, nil, nil,
		errors.New("open events.ndjson: permission denied"), 0, 70))
	if !strings.Contains(out, "events unavailable") || !strings.Contains(out, "permission denied") {
		t.Errorf("a failed event read should degrade one line; got:\n%s", out)
	}
	if strings.Contains(out, "no agent events yet") {
		t.Errorf("a failed read must not read as an empty log; got:\n%s", out)
	}
}

// Every line is padded (never truncated past) the width it is given, so the
// main area's right edge stays straight.
func TestRenderDetailFitsItsWidth(t *testing.T) {
	row := control.Row{Kind: control.RowSandbox, Project: "alpha",
		Name:  "a-very-long-sandbox-name-that-will-not-fit-in-forty-columns",
		State: control.StateRunning, Agent: control.AgentStatus{Reachable: true, State: "idle"}}
	for _, line := range strings.Split(plain(renderDetail(row, liveState{}, nil, nil, nil, nil, 0, 40)), "\n") {
		if len([]rune(line)) > 40 {
			t.Errorf("line wider than 40: %q", line)
		}
	}
}

// TestRenderDetailEventsNameTheirKind — the supervisor's own events
// (supervisor-start, user-turn, interrupt, sdk-ended) carry no data.type, so a
// band that rendered only the type printed a bare timestamp for every one of
// them. That is all a freshly booted sandbox has to show.
func TestRenderDetailEventsNameTheirKind(t *testing.T) {
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury",
		State: control.StateRunning}
	events := []control.EventLine{
		{Ts: "2026-09-18T21:04:23Z", Kind: "supervisor-start"},
		{Ts: "2026-09-18T21:05:01Z", Kind: "user-turn"},
		{Ts: "2026-09-18T21:05:02Z", Kind: "sdk-event", Type: "assistant"},
		{Ts: "2026-09-18T21:05:09Z", Kind: "sdk-event", Type: "result", Subtype: "success"},
	}
	out := plain(renderDetail(row, liveState{}, nil, nil, events, nil, 0, 70))
	for _, want := range []string{
		"supervisor-start", "user-turn", "sdk-event", "assistant", "result/success",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("event tail missing %q; got:\n%s", want, out)
		}
	}
	// A bare timestamp with nothing after it is the bug this guards.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "21:0") &&
			strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "21:04:23")) == "" {
			t.Errorf("event line renders as a bare timestamp: %q", line)
		}
	}
}
