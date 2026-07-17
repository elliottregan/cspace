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

// TestParseSystemStatus is a pure unit test — no container CLI required.
// Guards against the substring false positive where "apiserver is not
// running" was read as healthy because it contains "running".
func TestParseSystemStatus(t *testing.T) {
	table012 := `FIELD              VALUE
status             running
appRoot            /Users/dev/Library/Application Support/com.apple.container/
installRoot        /opt/homebrew/Cellar/container/0.12.3/
logRoot
apiserver.version  container-apiserver version 0.12.3 (build: release, commit: unspeci)
apiserver.commit   unspecified
apiserver.build    release
apiserver.appName  container-apiserver`

	cases := []struct {
		name    string
		output  string
		healthy bool
	}{
		{"0.12.x table, status running", table012, true},
		{"0.12.x table, status stopped",
			strings.Replace(table012, "status             running", "status             stopped", 1), false},
		{"prose affirmative", "apiserver is running", true},
		{"prose negated", "apiserver is not running", false},
		{"prose negated, mixed case",
			"apiserver is NOT running. Start it with `container system start`.", false},
		{"empty output", "", false},
		{"unrecognized output", "everything is fine", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := parseSystemStatus(tc.output)
			if tc.healthy && err != nil {
				t.Fatalf("expected healthy, got error: %v", err)
			}
			if !tc.healthy && err == nil {
				t.Fatal("expected unhealthy, got nil error")
			}
		})
	}
}

func TestHealthCheckRunning(t *testing.T) {
	requireContainerCLI(t)
	a := New()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.HealthCheck(ctx); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
}

// TestVersionMatchesSupported logs the locally-installed Apple Container
// CLI version. Does NOT fail on supported=false — this test runs on dev
// machines where the user may have moved off the tested range; we want to
// know about drift, not block CI. The warning path in cspace up is the
// user-facing surface for that.
func TestVersionMatchesSupported(t *testing.T) {
	requireContainerCLI(t)
	a := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	version, supported, err := a.VersionStatus(ctx)
	if err != nil {
		t.Fatalf("VersionStatus: %v", err)
	}
	t.Logf("Apple Container version: %q (supported=%v, tested=%s.x)",
		version, supported, SupportedMinorVersion())
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
