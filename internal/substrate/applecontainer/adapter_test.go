package applecontainer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/substrate"
)

// RunSpec is aliased here so test code reads cleanly without naming the
// substrate package on every call site.
type RunSpec = substrate.RunSpec

// requireContainerCLI skips the test if `container` is not on PATH.
func requireContainerCLI(t *testing.T) {
	t.Helper()
	a := New()
	if !a.Available() {
		t.Skip("Apple Container CLI not installed; skipping integration test")
	}
}

func TestRunAndExecAlpine(t *testing.T) {
	requireContainerCLI(t)
	a := New()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	name := "cspace-test-" + randSuffix()
	t.Cleanup(func() { _ = a.Stop(context.Background(), name) })

	if err := a.Run(ctx, RunSpec{
		Name:    name,
		Image:   "docker.io/library/alpine:latest",
		Command: []string{"sleep", "30"},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	res, err := a.Exec(ctx, name, []string{"echo", "hello"}, substrate.ExecOpts{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !strings.Contains(res.Stdout, "hello") {
		t.Fatalf("expected stdout to contain 'hello', got %q", res.Stdout)
	}
	if res.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", res.ExitCode)
	}
}

func TestStopIsIdempotent(t *testing.T) {
	requireContainerCLI(t)
	a := New()
	ctx := context.Background()
	if err := a.Stop(ctx, "cspace-nonexistent-"+randSuffix()); err != nil {
		t.Fatalf("Stop on missing should be no-op, got: %v", err)
	}
}

func TestIP(t *testing.T) {
	requireContainerCLI(t)
	a := New()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := "cspace-test-" + randSuffix()
	t.Cleanup(func() { _ = a.Stop(context.Background(), name) })

	if err := a.Run(ctx, RunSpec{
		Name:    name,
		Image:   "docker.io/library/alpine:latest",
		Command: []string{"sleep", "30"},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	ip, err := a.IP(ctx, name)
	if err != nil {
		t.Fatalf("IP: %v", err)
	}
	if ip == "" || !strings.Contains(ip, ".") {
		t.Fatalf("unexpected IP: %q", ip)
	}
}
