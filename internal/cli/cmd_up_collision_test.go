package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// stubContainerState scripts the two seams ensureSandboxAvailable uses — one
// inspect and one removal — and returns the name the inspect was asked about
// plus the list of names the guard asked to remove.
func stubContainerState(t *testing.T, exists bool, state string, err error) (probed *string, removed *[]string) {
	t.Helper()
	prevState, prevRemove := sandboxContainerState, sandboxContainerRemove
	t.Cleanup(func() {
		sandboxContainerState, sandboxContainerRemove = prevState, prevRemove
	})
	probed, removed = new(string), new([]string)
	sandboxContainerState = func(_ context.Context, name string) (bool, string, error) {
		*probed = name
		return exists, state, err
	}
	sandboxContainerRemove = func(_ context.Context, name string) error {
		*removed = append(*removed, name)
		return nil
	}
	return probed, removed
}

func TestEnsureSandboxAvailablePassesWhenNameIsFree(t *testing.T) {
	_, removed := stubContainerState(t, false, "", nil)

	if err := ensureSandboxAvailable(context.Background(), io.Discard, "resume-redux", "mercury"); err != nil {
		t.Fatalf("ensureSandboxAvailable() = %v, want nil", err)
	}
	if len(*removed) != 0 {
		t.Errorf("removed %v, want nothing touched when the name is free", *removed)
	}
}

// Destruction is fail-closed: only a state the substrate actually reported as
// "stopped" reclaims a name. Everything else — a state word that means
// something is live or in motion, a record with no state at all, or an
// inspect that could not be run — refuses with the same message, and removes
// nothing.
//
// The running case is the one the 2026-08-17 finding turns on: a running
// sandbox's baked control token has to survive a stray `cspace up`, and it
// cannot survive `container rm --force`.
func TestEnsureSandboxAvailableOnlyReclaimsAStoppedContainer(t *testing.T) {
	cases := []struct {
		name     string
		exists   bool
		state    string
		inspectE error
		reclaim  bool
	}{
		{name: "stopped", exists: true, state: "stopped", reclaim: true},
		{name: "stopped in another case", exists: true, state: "Stopped", reclaim: true},
		{name: "running", exists: true, state: "running"},
		{name: "stopping", exists: true, state: "stopping"},
		{name: "creating", exists: true, state: "creating"},
		{name: "no state word at all", exists: true, state: ""},
		{name: "unparseable inspect output", inspectE: errors.New("parse `container inspect`: unexpected end of JSON input")},
		{name: "inspect failed", inspectE: errors.New("container inspect: exit status 1")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, removed := stubContainerState(t, tc.exists, tc.state, tc.inspectE)

			err := ensureSandboxAvailable(context.Background(), io.Discard, "resume-redux", "mercury")
			if tc.reclaim {
				if err != nil {
					t.Fatalf("ensureSandboxAvailable() = %v, want a stopped container reclaimed", err)
				}
				if len(*removed) != 1 || (*removed)[0] != "cspace-resume-redux-mercury" {
					t.Fatalf("removed %v, want the stopped container removed once", *removed)
				}
				return
			}
			if err == nil {
				t.Fatal("ensureSandboxAvailable() = nil, want the name refused")
			}
			if len(*removed) != 0 {
				t.Fatalf("removed %v — only a container the substrate called stopped may be destroyed", *removed)
			}
			if msg := err.Error(); !strings.Contains(msg, "already exists") {
				t.Errorf("error %q should carry the existing already-exists message", msg)
			}
		})
	}
}

// The guarantee the 2026-08-17 finding rests on, asserted on its own: a
// running container is never removed, and the message says what to do next.
func TestEnsureSandboxAvailableNeverRemovesARunningContainer(t *testing.T) {
	// `container run -d --name` fails on a duplicate name and the adapter has
	// no adopt path, so this boot can never succeed. Failing here — before the
	// early registry write — is what keeps a running sandbox's control token
	// intact: the registry write would otherwise replace it with a token the
	// running supervisor has never seen, breaking `send` and `agent` against a
	// perfectly healthy sandbox.
	probed, removed := stubContainerState(t, true, "running", nil)

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

// A failed inspect refuses the name and says so: the boot stops, nothing is
// removed, and the reason the state could not be read rides along so the
// person can tell a sick substrate from a name that is genuinely taken.
func TestEnsureSandboxAvailableRefusesWhenTheStateCannotBeRead(t *testing.T) {
	_, removed := stubContainerState(t, false, "", errors.New("apiserver is not running"))

	err := ensureSandboxAvailable(context.Background(), io.Discard, "resume-redux", "mercury")
	if err == nil {
		t.Fatal("ensureSandboxAvailable() = nil, want an unreadable state to refuse the name")
	}
	if len(*removed) != 0 {
		t.Fatalf("removed %v — an unreadable state is not permission to destroy anything", *removed)
	}
	if msg := err.Error(); !strings.Contains(msg, "apiserver is not running") {
		t.Errorf("error %q should carry why the state could not be read", msg)
	}
}

// A stopped container of the same name holds nothing worth protecting — its
// control token is already dead — while `container run --name` still refuses
// the name. Refusing here made a stopped sandbox unbootable from the
// dashboard, whose boot key is only ever offered on stopped rows and whose
// rows come from registry entries `cspace down` removes.
//
// Because the reclaim destroys the container, the notice has to say so.
func TestEnsureSandboxAvailableReclaimsAStoppedContainer(t *testing.T) {
	_, removed := stubContainerState(t, true, "stopped", nil)

	var out bytes.Buffer
	if err := ensureSandboxAvailable(context.Background(), &out, "cspace", "issue-142"); err != nil {
		t.Fatalf("ensureSandboxAvailable() = %v, want a stopped container reclaimed", err)
	}
	if len(*removed) != 1 || (*removed)[0] != "cspace-cspace-issue-142" {
		t.Errorf("removed %v, want the stopped container removed once", *removed)
	}
	notice := out.String()
	if !strings.Contains(notice, "cspace-cspace-issue-142") {
		t.Errorf("output %q should name the container it reclaimed", notice)
	}
	for _, want := range []string{"destroys", "bind mounts"} {
		if !strings.Contains(notice, want) {
			t.Errorf("output %q should say the container and its writable layer are destroyed (missing %q)", notice, want)
		}
	}
}

// A removal that fails has to stop the boot: proceeding would hit the same
// duplicate-name refusal from `container run`, but after the early registry
// write has already replaced the entry.
func TestEnsureSandboxAvailableFailsWhenReclaimFails(t *testing.T) {
	stubContainerState(t, true, "stopped", nil)
	sandboxContainerRemove = func(context.Context, string) error { return errors.New("boom") }

	err := ensureSandboxAvailable(context.Background(), io.Discard, "cspace", "issue-142")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("ensureSandboxAvailable() = %v, want the removal failure surfaced", err)
	}
}

func TestEnsureSandboxAvailableNamesTheRightContainerPerProject(t *testing.T) {
	probed, _ := stubContainerState(t, false, "", nil)

	if err := ensureSandboxAvailable(context.Background(), io.Discard, "cspace", "issue-142"); err != nil {
		t.Fatalf("ensureSandboxAvailable() = %v, want nil", err)
	}
	if *probed != "cspace-cspace-issue-142" {
		t.Errorf("probed %q, want cspace-cspace-issue-142", *probed)
	}
}

// containerState's own contract: the three outcomes are distinguishable, and
// the one non-zero exit that means "the name is free" is the substrate's
// not-found message and nothing else.
func TestIsContainerNotFoundRecognizesTheSubstrateMessages(t *testing.T) {
	notFound := []string{
		"Error: container not found: cspace-alpha-mercury\n", // 1.x inspect
		"Error: notFound", // `container rm`
	}
	for _, s := range notFound {
		if !isContainerNotFound(s) {
			t.Errorf("isContainerNotFound(%q) = false, want true", s)
		}
	}
	live := []string{
		"Error: apiserver is not running\n",
		"",
		"Error: internal error",
	}
	for _, s := range live {
		if isContainerNotFound(s) {
			t.Errorf("isContainerNotFound(%q) = true — a sick substrate must not read as a free name", s)
		}
	}
}
