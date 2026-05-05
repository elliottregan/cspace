package orchestrator

import (
	"context"
	"testing"
	"time"

	v2 "github.com/elliottregan/cspace/internal/compose/v2"
)

func TestHealthcheckPollPasses(t *testing.T) {
	calls := 0
	exec := func(_ context.Context, _ []string) (string, int, error) {
		calls++
		if calls < 3 {
			return "", 1, nil
		}
		return "ok", 0, nil
	}
	hc := &v2.Healthcheck{
		Test:     []string{"CMD", "true"},
		Interval: 10 * time.Millisecond,
		Timeout:  100 * time.Millisecond,
		Retries:  5,
	}
	if err := waitHealthy(context.Background(), hc, exec); err != nil {
		t.Fatalf("want pass, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls=%d, want 3", calls)
	}
}

func TestHealthcheckPollFails(t *testing.T) {
	exec := func(_ context.Context, _ []string) (string, int, error) {
		return "", 1, nil
	}
	hc := &v2.Healthcheck{
		Test:     []string{"CMD", "true"},
		Interval: 1 * time.Millisecond,
		Timeout:  100 * time.Millisecond,
		Retries:  3,
	}
	if err := waitHealthy(context.Background(), hc, exec); err == nil {
		t.Fatal("want failure, got nil")
	}
}

func TestHealthcheckNilSkips(t *testing.T) {
	if err := waitHealthy(context.Background(), nil, nil); err != nil {
		t.Fatalf("nil hc should noop, got %v", err)
	}
}

func TestNormalizeHealthcheckCmd(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"CMD form", []string{"CMD", "curl", "-f", "http://x"}, []string{"curl", "-f", "http://x"}},
		{"CMD-SHELL form", []string{"CMD-SHELL", "curl -f http://x"}, []string{"sh", "-c", "curl -f http://x"}},
		{"NONE disables", []string{"NONE"}, nil},
		{"bare string", []string{"curl -f http://x"}, []string{"sh", "-c", "curl -f http://x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeHealthcheckCmd(c.in)
			if !equalSlices(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
