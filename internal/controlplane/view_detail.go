package controlplane

import (
	"fmt"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/control"
)

// detailEvents is how many event-log lines the band shows.
const detailEvents = 8

// renderDetail renders the selected row's detail band: uptime, memory, agent
// session and last event, the labeled URLs, and the tail of the supervisor's
// event log — everything the 24-column sidebar has no room for.
//
// It takes an explicit width so one renderer serves both placements: the
// main area now, and the narrow strip under the sidebar once panes take the
// main area over in rollout step 4.
func renderDetail(row control.Row, live liveState, ports []control.Port, portsErr error, events []control.EventLine, eventsErr error, memoryUsedB int64, width int) string {
	var lines []string
	add := func(style lipglossStyle, format string, args ...any) {
		lines = append(lines, style.Render(fit(fmt.Sprintf(format, args...), width)))
	}

	switch row.Kind {
	case control.RowSandbox:
		add(lipglossStyle{}, "%s · %s · %s · %s", row.Name, stateLabel(row),
			formatUptime(row.Uptime), formatMemUsage(memoryUsedB, row.MemoryB))
		if row.State == control.StateStopped {
			add(styleDim, "not running — press u to boot it, or select another sandbox")
			return strings.Join(lines, "\n")
		}
		if row.IP != "" {
			add(styleDim, "%s · %s", row.Container, row.IP)
		}

		if a := agentOf(row, live); a.Reachable {
			add(lipglossStyle{}, "agent: %s · session %s · queue %d · last event %s",
				a.State, sessionOr(a.Session), a.QueueDepth, lastEventLabel(a))
		} else {
			add(styleErr, "agent: supervisor unreachable — send and interrupt are off")
		}
		if is := live.Interactive; is.Known() {
			add(lipglossStyle{}, "claude: %s · %s · %s", is.State, is.Event, shortTs(is.At))
		}

		lines = append(lines, "")
		switch {
		case portsErr != nil:
			add(styleDim, "ports unavailable: %v", portsErr)
		case len(ports) == 0:
			add(styleDim, "no listening ports")
		default:
			for _, p := range ports {
				add(stylePort.Hyperlink(p.URL), "  %-6d %-12s %s", p.Port, p.Label, p.URL)
			}
		}

		lines = append(lines, "")
		switch {
		case eventsErr != nil:
			// Degrade the same way a failed ports probe does: an unreadable
			// log is not an empty one, and saying "no events yet" for a
			// sandbox whose events.ndjson could not be opened hides the
			// only clue there is.
			add(styleDim, "events unavailable: %v", eventsErr)
		case len(events) == 0:
			add(styleDim, "no agent events yet")
		default:
			add(styleDim, "recent events")
			for _, e := range tailEvents(events, detailEvents) {
				add(styleDim, "  %s %-16s %s", shortTs(e.Ts), eventKind(e), eventDetail(e))
			}
		}

	case control.RowBrowser:
		add(lipglossStyle{}, "%s · %s", row.Name, stateLabel(row))
		if row.Browser.Reachable {
			version := row.Browser.Version
			if version == "" {
				version = "reachable"
			}
			add(styleOK, "CDP :%d · %s", control.BrowserCDPPort, version)
		} else {
			add(styleErr, "CDP :%d unreachable — press b to restart the sidecar",
				control.BrowserCDPPort)
		}
		if row.IP != "" {
			add(styleDim, "%s · %s", row.Container, row.IP)
		}

	case control.RowSidecar, control.RowSystem:
		add(lipglossStyle{}, "%s · %s", row.Name, stateLabel(row))
		// A stopped sidecar has no address. Drop the segment rather than
		// print an empty one between two separators, the way the sandbox and
		// browser branches above do.
		if row.IP != "" {
			add(styleDim, "%s · %s · %s", row.Container, row.IP,
				formatMemUsage(memoryUsedB, row.MemoryB))
		} else {
			add(styleDim, "%s · %s", row.Container, formatMemUsage(memoryUsedB, row.MemoryB))
		}

	default:
		add(styleDim, "select a sandbox")
	}
	return strings.Join(lines, "\n")
}

// eventKind names one event-log record. Every line the supervisor writes
// carries a kind — supervisor-start, supervisor-resume, agent-role,
// agent-model, user-turn, interrupt, sdk-event, sdk-error, sdk-ended — but
// only sdk-event records carry the SDK message's type/subtype in their data.
// Rendering the type alone therefore printed a bare timestamp for every event
// the supervisor emits about itself, which is all there is until the agent
// takes its first turn.
func eventKind(e control.EventLine) string {
	if e.Kind != "" {
		return e.Kind
	}
	if e.Type != "" {
		return e.Type
	}
	return "event"
}

// eventDetail is the sdk-event payload's type/subtype, empty for the kinds
// that carry no such data.
func eventDetail(e control.EventLine) string {
	if e.Kind == "" || e.Type == "" {
		// Kind already showed the type; don't repeat it.
		return ""
	}
	if e.Subtype != "" {
		return e.Type + "/" + e.Subtype
	}
	return e.Type
}

// tailEvents keeps the last n of what the reader returned. control.Events
// already bounds its read; this bounds what a narrow band shows.
func tailEvents(events []control.EventLine, n int) []control.EventLine {
	if len(events) <= n {
		return events
	}
	return events[len(events)-n:]
}

func sessionOr(session string) string {
	if session == "" {
		return "-"
	}
	return session
}

// stateLabel is the status word a row prints, derived from State so a
// stopped or degraded sidecar, browser or system container is not
// mislabeled "running".
func stateLabel(r control.Row) string {
	switch r.State {
	case control.StateRunning:
		return "running"
	case control.StateDegraded:
		return "degraded"
	case control.StateBooting:
		return "booting"
	}
	return "stopped"
}

// formatMemory renders bytes as a compact G/M string; 0 is "-".
func formatMemory(b int64) string {
	switch {
	case b <= 0:
		return "-"
	case b >= 1<<30:
		return fmt.Sprintf("%dG", b/(1<<30))
	default:
		return fmt.Sprintf("%dM", b/(1<<20))
	}
}

// formatMemUsage renders live usage against the cap as "<used>/<cap>",
// falling back to the cap alone when there is no sample — a row never loses
// information it used to show. Usage keeps one decimal at GiB scale: whole
// gigabytes would render a 1.6G sandbox and a 1.1G one identically.
func formatMemUsage(usedB, capB int64) string {
	if usedB <= 0 {
		return formatMemory(capB)
	}
	var used string
	if usedB >= 1<<30 {
		used = fmt.Sprintf("%.1fG", float64(usedB)/float64(int64(1)<<30))
	} else {
		used = fmt.Sprintf("%dM", usedB/(1<<20))
	}
	if capB <= 0 {
		return used
	}
	return used + "/" + formatMemory(capB)
}

// formatUptime renders a duration as ↑<h>h<m>m / ↑<m>m / ↑<s>s.
func formatUptime(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("↑%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("↑%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("↑%ds", int(d.Seconds()))
	}
}

// formatAge renders how long ago t was, for the footer's staleness marker.
func formatAge(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm ago", int(d.Minutes()))
}

func lastEventLabel(a control.AgentStatus) string {
	if a.LastEventType == "" {
		return "-"
	}
	if a.LastEventSubtype != "" {
		return a.LastEventType + "/" + a.LastEventSubtype
	}
	return a.LastEventType
}

// shortTs is HH:MM:SS out of an ISO 8601 timestamp, passed through
// unchanged when it is not one.
func shortTs(ts string) string {
	if len(ts) >= 19 {
		return ts[11:19]
	}
	return ts
}
