package applecontainer

import (
	"context"
	"os"
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

// requireE2E gates tests that create real containers on the host behind an
// explicit opt-in, so a default `go test ./...` is free of host side effects.
func requireE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("CSPACE_E2E") == "" {
		t.Skip("creates real containers on the host; set CSPACE_E2E=1 to run")
	}
}

func TestRunAndExecAlpine(t *testing.T) {
	requireContainerCLI(t)
	requireE2E(t)
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
	requireE2E(t)
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

// TestParseContainerList exercises the Apple Container 1.1.x `container ls
// --all --format json` shape: runtime state (state, startedDate, networks)
// is nested under a `status` object, and startedDate is an RFC3339 string
// rather than the flat CFAbsoluteTime float 0.12.x emitted.
func TestParseContainerList(t *testing.T) {
	const fixture = `[
	  {"id":"cspace-demo-mercury",
	   "configuration":{"id":"cspace-demo-mercury",
	     "image":{"reference":"cspace:latest"},
	     "resources":{"cpus":4,"memoryInBytes":17179869184}},
	   "status":{"state":"running","startedDate":"2026-07-21T02:32:47Z",
	     "networks":[{"ipv4Address":"192.168.64.108/24","network":"default"}]}},
	  {"id":"buildkit",
	   "configuration":{"id":"buildkit",
	     "image":{"reference":"ghcr.io/apple/builder:0.12.0"},
	     "resources":{"cpus":2,"memoryInBytes":2147483648}},
	   "status":{"state":"stopped","networks":[]}}
	]`
	got, err := parseContainerList(fixture)
	if err != nil {
		t.Fatalf("parseContainerList: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	m := got[0]
	if m.Name != "cspace-demo-mercury" || m.State != "running" || m.IP != "192.168.64.108" {
		t.Errorf("record0 = %+v", m)
	}
	if m.CPUs != 4 || m.MemoryB != 17179869184 || m.Image != "cspace:latest" {
		t.Errorf("record0 fields = %+v", m)
	}
	if want := "2026-07-21T02:32:47Z"; m.Started.UTC().Format(time.RFC3339) != want {
		t.Errorf("Started = %s, want %s", m.Started.UTC().Format(time.RFC3339), want)
	}
	if got[1].IP != "" {
		t.Errorf("record1 (no networks) IP = %q, want empty", got[1].IP)
	}
	if got[1].State != "stopped" {
		t.Errorf("record1 State = %q, want stopped", got[1].State)
	}
	// A record whose status omits startedDate must parse to the zero time,
	// not error the whole list.
	if !got[1].Started.IsZero() {
		t.Errorf("record1 Started = %v, want zero time", got[1].Started)
	}
}

// TestParseInspectIPv4 exercises the Apple Container 1.1.x `container inspect`
// shape, where networks (and thus ipv4Address) are nested under a `status`
// object. Guards both Adapter.IP and the daemon's sidecar DNS resolver, which
// share this parser.
func TestParseInspectIPv4(t *testing.T) {
	const running = `[{"id":"cspace-demo-mercury",
	  "configuration":{"id":"cspace-demo-mercury"},
	  "status":{"state":"running",
	    "networks":[{"ipv4Address":"192.168.64.13/24","network":"default"}]}}]`
	ip, err := ParseInspectIPv4([]byte(running))
	if err != nil {
		t.Fatalf("ParseInspectIPv4: %v", err)
	}
	if ip != "192.168.64.13" {
		t.Errorf("ip = %q, want 192.168.64.13", ip)
	}

	// No records (missing/gone container): ("", nil), not an error.
	if ip, err := ParseInspectIPv4([]byte(`[]`)); err != nil || ip != "" {
		t.Errorf("empty: ip=%q err=%v, want \"\" nil", ip, err)
	}

	// Record present but no IPv4 yet (container still coming up): ("", nil).
	const noIP = `[{"id":"x","status":{"state":"running","networks":[]}}]`
	if ip, err := ParseInspectIPv4([]byte(noIP)); err != nil || ip != "" {
		t.Errorf("noIP: ip=%q err=%v, want \"\" nil", ip, err)
	}

	// Malformed JSON is the only error case.
	if _, err := ParseInspectIPv4([]byte(`{not json`)); err == nil {
		t.Error("want error for malformed JSON, got nil")
	}
}

func TestParseContainerListEmpty(t *testing.T) {
	got, err := parseContainerList(`[]`)
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

func TestParseContainerListMalformed(t *testing.T) {
	if _, err := parseContainerList(`{not json`); err == nil {
		t.Fatal("want error for malformed JSON, got nil")
	}
}

func TestListLive(t *testing.T) {
	requireContainerCLI(t)
	requireE2E(t)
	a := New()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	got, err := a.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// buildkit is essentially always present on a dev host running the substrate.
	t.Logf("List returned %d containers", len(got))
	for _, c := range got {
		if c.Name == "" {
			t.Errorf("container with empty Name: %+v", c)
		}
	}
}
