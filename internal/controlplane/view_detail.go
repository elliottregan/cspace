package controlplane

import (
	"fmt"
	"time"

	"github.com/elliottregan/cspace/internal/control"
)

// detailEvents bounds the recent events included in container Details.
const detailEvents = 8

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
// already bounds its read; this bounds the dialog's recent-event section.
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

// shortTs is HH:MM:SS out of an ISO 8601 timestamp, passed through
// unchanged when it is not one.
func shortTs(ts string) string {
	if len(ts) >= 19 {
		return ts[11:19]
	}
	return ts
}
