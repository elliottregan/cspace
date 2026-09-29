package controlplane

import (
	"testing"
	"time"

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

func TestEventLabelsRetainSupervisorAndSDKInformation(t *testing.T) {
	for _, tc := range []struct {
		event        control.EventLine
		kind, detail string
	}{
		{control.EventLine{Kind: "supervisor-start"}, "supervisor-start", ""},
		{control.EventLine{Kind: "user-turn"}, "user-turn", ""},
		{control.EventLine{Kind: "sdk-event", Type: "assistant"}, "sdk-event", "assistant"},
		{control.EventLine{Kind: "sdk-event", Type: "result", Subtype: "success"}, "sdk-event", "result/success"},
		{control.EventLine{Type: "legacy"}, "legacy", ""},
		{control.EventLine{}, "event", ""},
	} {
		if got := eventKind(tc.event); got != tc.kind {
			t.Errorf("kind=%q, want %q", got, tc.kind)
		}
		if got := eventDetail(tc.event); got != tc.detail {
			t.Errorf("detail=%q, want %q", got, tc.detail)
		}
	}
}
