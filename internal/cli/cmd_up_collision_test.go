package cli

import (
	"context"
	"strings"
	"testing"
)

func TestEnsureSandboxAvailablePassesWhenNameIsFree(t *testing.T) {
	prev := sandboxContainerExists
	t.Cleanup(func() { sandboxContainerExists = prev })
	sandboxContainerExists = func(context.Context, string) bool { return false }

	if err := ensureSandboxAvailable(context.Background(), "resume-redux", "mercury"); err != nil {
		t.Fatalf("ensureSandboxAvailable() = %v, want nil", err)
	}
}

func TestEnsureSandboxAvailableRejectsAnExistingContainer(t *testing.T) {
	// `container run -d --name` fails on a duplicate name and the adapter has
	// no adopt path, so this boot can never succeed. Failing here — before the
	// early registry write — is what keeps a running sandbox's control token
	// intact: the registry write would otherwise replace it with a token the
	// running supervisor has never seen, breaking `send` and `agent` against a
	// perfectly healthy sandbox.
	prev := sandboxContainerExists
	t.Cleanup(func() { sandboxContainerExists = prev })

	var probed string
	sandboxContainerExists = func(_ context.Context, name string) bool {
		probed = name
		return true
	}

	err := ensureSandboxAvailable(context.Background(), "resume-redux", "mercury")
	if err == nil {
		t.Fatal("ensureSandboxAvailable() = nil, want an error for an existing container")
	}
	if probed != "cspace-resume-redux-mercury" {
		t.Errorf("probed %q, want the canonical container name", probed)
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

func TestEnsureSandboxAvailableNamesTheRightContainerPerProject(t *testing.T) {
	prev := sandboxContainerExists
	t.Cleanup(func() { sandboxContainerExists = prev })

	var probed string
	sandboxContainerExists = func(_ context.Context, name string) bool {
		probed = name
		return false
	}
	if err := ensureSandboxAvailable(context.Background(), "cspace", "issue-142"); err != nil {
		t.Fatalf("ensureSandboxAvailable() = %v, want nil", err)
	}
	if probed != "cspace-cspace-issue-142" {
		t.Errorf("probed %q, want cspace-cspace-issue-142", probed)
	}
}
