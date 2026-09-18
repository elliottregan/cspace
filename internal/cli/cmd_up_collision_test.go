package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// stubContainerState scripts the three seams ensureSandboxAvailable uses and
// returns a pointer to the list of names it was asked to remove.
func stubContainerState(t *testing.T, exists, running bool) (probed *string, removed *[]string) {
	t.Helper()
	prevExists, prevRunning, prevRemove := sandboxContainerExists, sandboxContainerRunning, sandboxContainerRemove
	t.Cleanup(func() {
		sandboxContainerExists, sandboxContainerRunning, sandboxContainerRemove = prevExists, prevRunning, prevRemove
	})
	probed, removed = new(string), new([]string)
	sandboxContainerExists = func(_ context.Context, name string) bool {
		*probed = name
		return exists
	}
	sandboxContainerRunning = func(context.Context, string) bool { return running }
	sandboxContainerRemove = func(_ context.Context, name string) error {
		*removed = append(*removed, name)
		return nil
	}
	return probed, removed
}

func TestEnsureSandboxAvailablePassesWhenNameIsFree(t *testing.T) {
	_, removed := stubContainerState(t, false, false)

	if err := ensureSandboxAvailable(context.Background(), io.Discard, "resume-redux", "mercury"); err != nil {
		t.Fatalf("ensureSandboxAvailable() = %v, want nil", err)
	}
	if len(*removed) != 0 {
		t.Errorf("removed %v, want nothing touched when the name is free", *removed)
	}
}

func TestEnsureSandboxAvailableRejectsARunningContainer(t *testing.T) {
	// `container run -d --name` fails on a duplicate name and the adapter has
	// no adopt path, so this boot can never succeed. Failing here — before the
	// early registry write — is what keeps a running sandbox's control token
	// intact: the registry write would otherwise replace it with a token the
	// running supervisor has never seen, breaking `send` and `agent` against a
	// perfectly healthy sandbox.
	probed, removed := stubContainerState(t, true, true)

	err := ensureSandboxAvailable(context.Background(), io.Discard, "resume-redux", "mercury")
	if err == nil {
		t.Fatal("ensureSandboxAvailable() = nil, want an error for a running container")
	}
	if *probed != "cspace-resume-redux-mercury" {
		t.Errorf("probed %q, want the canonical container name", *probed)
	}
	if len(*removed) != 0 {
		t.Fatalf("removed %v — a running sandbox must never be reclaimed", *removed)
	}
	msg := err.Error()
	for _, want := range []string{"mercury", "cspace attach mercury", "cspace down mercury"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q — the message has to say what to do next", msg, want)
		}
	}
	if strings.Contains(msg, "substrate run") {
		t.Errorf("error %q leaks the raw substrate failure; the guard exists to replace it", msg)
	}
}

// A stopped container of the same name holds nothing worth protecting — its
// control token is already dead — while `container run --name` still refuses
// the name. Refusing here made a stopped sandbox unbootable from the
// dashboard, whose boot key is only ever offered on stopped rows and whose
// rows come from registry entries `cspace down` removes.
func TestEnsureSandboxAvailableReclaimsAStoppedContainer(t *testing.T) {
	_, removed := stubContainerState(t, true, false)

	var out bytes.Buffer
	if err := ensureSandboxAvailable(context.Background(), &out, "cspace", "issue-142"); err != nil {
		t.Fatalf("ensureSandboxAvailable() = %v, want a stopped container reclaimed", err)
	}
	if len(*removed) != 1 || (*removed)[0] != "cspace-cspace-issue-142" {
		t.Errorf("removed %v, want the stopped container removed once", *removed)
	}
	if !strings.Contains(out.String(), "cspace-cspace-issue-142") {
		t.Errorf("output %q should name the container it reclaimed", out.String())
	}
}

// A removal that fails has to stop the boot: proceeding would hit the same
// duplicate-name refusal from `container run`, but after the early registry
// write has already replaced the entry.
func TestEnsureSandboxAvailableFailsWhenReclaimFails(t *testing.T) {
	stubContainerState(t, true, false)
	sandboxContainerRemove = func(context.Context, string) error { return errors.New("boom") }

	err := ensureSandboxAvailable(context.Background(), io.Discard, "cspace", "issue-142")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("ensureSandboxAvailable() = %v, want the removal failure surfaced", err)
	}
}

func TestEnsureSandboxAvailableNamesTheRightContainerPerProject(t *testing.T) {
	probed, _ := stubContainerState(t, false, false)

	if err := ensureSandboxAvailable(context.Background(), io.Discard, "cspace", "issue-142"); err != nil {
		t.Fatalf("ensureSandboxAvailable() = %v, want nil", err)
	}
	if *probed != "cspace-cspace-issue-142" {
		t.Errorf("probed %q, want cspace-cspace-issue-142", *probed)
	}
}
