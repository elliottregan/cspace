//go:build darwin

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
)

// TestCspaceLifecycle exercises the full sandbox lifecycle:
// up -> /health curl with bearer -> send -> events.ndjson -> down.
//
// Skipped when the substrate isn't ready (CLI missing, apiserver down,
// image not built). Skip messages identify the missing prerequisite so
// it's obvious what to fix.
//
// Does NOT require ANTHROPIC_API_KEY: the user-turn lands in events.ndjson
// regardless of whether Claude actually responds, which is the load-bearing
// signal here. The browser sidecar path (--browser) is intentionally out
// of scope for this test (slow, separate concern from lifecycle parity).
func TestCspaceLifecycle(t *testing.T) {
	a := applecontainer.New()
	if !a.Available() {
		t.Skip("Apple Container CLI not on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := a.HealthCheck(ctx); err != nil {
		t.Skipf("apiserver not running: %v", err)
	}
	if !imagePresent(t, "cspace:latest") {
		t.Skip("cspace:latest image not built; run `make cspace-image` first")
	}

	cspaceBin := findCspaceBinary(t)

	// Use a per-test sandbox name to avoid collisions with concurrent runs
	// or stale registry state from a prior aborted run.
	sandbox := "int-" + shortSuffix()
	t.Cleanup(func() {
		// Best-effort teardown — never fail the test on cleanup errors.
		_ = exec.Command(cspaceBin, "down", sandbox).Run()
		// Clean the auto-provisioned clone too so repeated runs don't
		// accumulate ~/.cspace/clones/cspace/int-* directories.
		home, _ := os.UserHomeDir()
		_ = os.RemoveAll(filepath.Join(home, ".cspace", "clones", "cspace", sandbox))
	})

	// 1. cspace up.
	upOut := runCspaceCmd(t, cspaceBin, "up", sandbox)
	if !strings.Contains(upOut, "sandbox "+sandbox+" up:") {
		t.Fatalf("cspace up output missing expected prefix:\n%s", upOut)
	}

	// 2. Registry has the entry, with a non-empty token + control URL.
	regPath, err := registry.DefaultPath()
	if err != nil {
		t.Fatalf("registry.DefaultPath: %v", err)
	}
	r := &registry.Registry{Path: regPath}
	entry, err := r.Lookup("cspace", sandbox)
	if err != nil {
		t.Fatalf("registry lookup: %v", err)
	}
	if entry.ControlURL == "" || entry.Token == "" {
		t.Fatalf("registry entry missing fields: %+v", entry)
	}

	// 3. /health curl with bearer auth — supervisor reachable.
	if err := waitHealth(ctx, entry.ControlURL+"/health", entry.Token, 30*time.Second); err != nil {
		t.Fatalf("waiting for /health: %v", err)
	}

	// 4. cspace send injects a user turn.
	const ping = "lifecycle integration test ping"
	sendOut := runCspaceCmd(t, cspaceBin, "send", sandbox, ping)
	if !strings.Contains(sendOut, `"ok":true`) {
		t.Fatalf("cspace send output missing ok:true:\n%s", sendOut)
	}

	// 5. user-turn event lands in events.ndjson inside the container.
	containerName := "cspace-cspace-" + sandbox
	if err := waitForUserTurn(ctx, containerName, ping, 30*time.Second); err != nil {
		t.Fatalf("waiting for user-turn event: %v", err)
	}

	// 6. cspace down tears the sandbox down.
	downOut := runCspaceCmd(t, cspaceBin, "down", sandbox)
	if !strings.Contains(downOut, "sandbox "+sandbox+" down") {
		t.Fatalf("cspace down output missing expected text:\n%s", downOut)
	}

	// 7. Registry entry removed.
	if _, err := r.Lookup("cspace", sandbox); err == nil {
		t.Fatalf("registry entry still present after cspace down")
	}
}

// imagePresent returns true if the named image (e.g. "cspace:latest") is in
// the local Apple Container store. The Apple `container` CLI splits NAME and
// TAG into separate columns, so we check that the parsed entries contain a
// row matching name and tag.
func imagePresent(t *testing.T, ref string) bool {
	t.Helper()
	name, tag, ok := strings.Cut(ref, ":")
	if !ok {
		tag = "latest"
	}
	out, err := exec.Command("container", "image", "list").CombinedOutput()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[0] == name && fields[1] == tag {
			return true
		}
	}
	// Defensive fallback: some `container` versions might format differently.
	// A substring match on the full ref still works for "name:tag" rows.
	return bytes.Contains(out, []byte(ref))
}

// findCspaceBinary returns the cspace binary to invoke. Looks for cspace-go on
// PATH first; falls back to the canonical Makefile output ../../bin/cspace-go
// (relative to internal/cli/). Fails the test if neither is found — it would
// be misleading to skip here since this is a build problem, not a substrate
// readiness problem.
func findCspaceBinary(t *testing.T) string {
	t.Helper()
	if path, err := exec.LookPath("cspace-go"); err == nil {
		return path
	}
	candidate := filepath.Join("..", "..", "bin", "cspace-go")
	if abs, err := filepath.Abs(candidate); err == nil {
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	t.Fatalf("cspace-go binary not found on PATH or at ../../bin/cspace-go; build with `make build`")
	return ""
}

// runCspaceCmd runs a cspace subcommand and returns its combined stdout/stderr.
// Fails the test if the command errors — every cspace invocation in this
// test is expected to succeed.
func runCspaceCmd(t *testing.T, bin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cspace %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// waitHealth polls /health with bearer auth until it returns 200 or the
// deadline passes. Necessary because the supervisor takes a brief moment
// to bind its control port after the container starts.
func waitHealth(ctx context.Context, url, token string, max time.Duration) error {
	deadline := time.Now().Add(max)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("/health did not respond in %s", max)
}

// waitForUserTurn polls events.ndjson inside the container for a user-turn
// event whose data.text contains the given string. Returns nil when found,
// error on timeout. Reads the file via `container exec cat` rather than a
// host path because /sessions is volume-managed by the substrate.
func waitForUserTurn(ctx context.Context, containerName, expectedText string, max time.Duration) error {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		out, err := exec.CommandContext(ctx, "container", "exec", containerName,
			"cat", "/sessions/primary/events.ndjson").CombinedOutput()
		if err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var e struct {
					Kind string                 `json:"kind"`
					Data map[string]interface{} `json:"data"`
				}
				if err := json.Unmarshal([]byte(line), &e); err != nil {
					continue
				}
				if e.Kind != "user-turn" {
					continue
				}
				if text, ok := e.Data["text"].(string); ok && strings.Contains(text, expectedText) {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("user-turn event with text %q not seen in %s", expectedText, max)
}

// shortSuffix returns a short numeric suffix derived from the current
// nanosecond timestamp. Sufficient for unique per-test sandbox names within
// a single test process; collisions across processes are extremely unlikely
// at this granularity and would surface as a clean Run() error rather than
// a silent corruption.
func shortSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%100000)
}
