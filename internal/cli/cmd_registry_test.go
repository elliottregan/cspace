package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
)

// TestInspectHasRecords guards containerExists against Apple Container 1.1.x's
// pretty-printed `container inspect` output. 1.1.x emits the array as
// "[\n  {\n ...", so a byte-prefix check against "[{" wrongly reports an
// existing container as missing — which would let the registry-prune paths
// tear down live sandboxes.
func TestInspectHasRecords(t *testing.T) {
	const prettyOneRecord = `[
  {
    "id" : "buildkit",
    "configuration" : { "id" : "buildkit" },
    "status" : { "state" : "running" }
  }
]`
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"pretty one record (1.1.x)", prettyOneRecord, true},
		{"compact one record (0.12.x)", `[{"id":"x"}]`, true},
		{"empty array", `[]`, false},
		{"empty array pretty", "[\n\n]", false},
		{"empty string", ``, false},
		{"malformed", `{not json`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := inspectHasRecords([]byte(tc.in)); got != tc.want {
				t.Errorf("inspectHasRecords(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// stubContainerAlive replaces the containerAlive seam for one test and
// restores it on cleanup, matching the stub helpers in cmd_daemon_test.go.
func stubContainerAlive(t *testing.T, fn func(ctx context.Context, name string) bool) {
	t.Helper()
	orig := containerAlive
	t.Cleanup(func() { containerAlive = orig })
	containerAlive = fn
}

// TestRegistryPruneKeepsAStoppedEntry — `cspace down --keep-state` removes
// the container and marks the entry stopped on purpose, so prune's
// "container is gone, therefore the entry is stale" rule would undo exactly
// what the flag promised. Worse, `cspace down --all`'s own skip message
// tells the operator to run this command.
func TestRegistryPruneKeepsAStoppedEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	regPath, err := registry.DefaultPath()
	if err != nil {
		t.Fatalf("registry.DefaultPath: %v", err)
	}
	r := &registry.Registry{Path: regPath}
	if err := r.Register(registry.Entry{
		Project: "demo", Name: "kept", State: registry.StateStopped, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed kept entry: %v", err)
	}
	if err := r.Register(registry.Entry{
		Project: "demo", Name: "stale", State: registry.StateReady,
		IP: "192.168.64.9", StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed stale entry: %v", err)
	}

	// Neither sandbox has a container: that is precisely what makes the
	// two indistinguishable without the state word.
	stubContainerAlive(t, func(context.Context, string) bool { return false })

	var out bytes.Buffer
	if err := runRegistryPrune(&out, false); err != nil {
		t.Fatalf("prune: %v", err)
	}

	if _, err := r.Lookup("demo", "kept"); err != nil {
		t.Errorf("prune removed the kept entry: %v", err)
	}
	if _, err := r.Lookup("demo", "stale"); err == nil {
		t.Error("prune left the stale entry behind")
	}
	if !strings.Contains(out.String(), "kept (stopped): demo:kept") {
		t.Errorf("output = %q, want it to report the kept entry", out.String())
	}
	if !strings.Contains(out.String(), "removed: demo:stale") {
		t.Errorf("output = %q, want it to report the stale entry as removed", out.String())
	}
}

// TestRegistryListNamesAKeptEntry — the LIFECYCLE column read "dead" for a
// kept entry, which is the same word a stale one gets. prune now says
// "kept (stopped)"; list has to agree, since it is where an operator looks
// before deciding whether to prune.
func TestRegistryListNamesAKeptEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	regPath, err := registry.DefaultPath()
	if err != nil {
		t.Fatalf("registry.DefaultPath: %v", err)
	}
	r := &registry.Registry{Path: regPath}
	if err := r.Register(registry.Entry{
		Project: "demo", Name: "kept", State: registry.StateStopped, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed kept entry: %v", err)
	}
	stubContainerAlive(t, func(context.Context, string) bool { return false })

	var out bytes.Buffer
	if err := runRegistryList(&out); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out.String(), "kept (stopped)") {
		t.Errorf("output = %q, want the kept entry's lifecycle column to say so", out.String())
	}
}
