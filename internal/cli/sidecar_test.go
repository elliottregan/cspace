package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
)

// errStub stands in for any failure the container CLI can return.
var errStub = errors.New("stub failure")

// stubContainerCLI replaces browserExecCmd — the single seam every substrate
// call in the restart ladder routes through — with a recorder. The handler
// receives the argv and returns (stdout, error).
func stubContainerCLI(t *testing.T, handler func(args []string) (string, error)) *[]string {
	t.Helper()
	var calls []string
	orig := browserExecCmd
	t.Cleanup(func() { browserExecCmd = orig })
	browserExecCmd = func(_ context.Context, name string, args ...string) (string, error) {
		calls = append(calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
		return handler(append([]string{name}, args...))
	}
	return &calls
}

const inspectRunning = `[{"status":{"state":"running","networks":[{"ipv4Address":"192.168.66.13/24","network":"default"}]}}]`
const inspectStopped = `[{"status":{"state":"stopped","networks":[]}}]`

func TestProjectSidecarName(t *testing.T) {
	got := projectSidecarName("resume-redux", "mercury", "convex-backend")
	if want := "cspace-resume-redux-mercury-convex-backend"; got != want {
		t.Errorf("projectSidecarName = %q, want %q", got, want)
	}
}

// TestRestartProjectSidecarStoppedContainer is the ordinary case from the
// 2026-08-28 incident: the sidecar died and is sitting there stopped.
func TestRestartProjectSidecarStoppedContainer(t *testing.T) {
	started := false
	calls := stubContainerCLI(t, func(args []string) (string, error) {
		switch args[1] {
		case "inspect":
			if started {
				return inspectRunning, nil
			}
			return inspectStopped, nil
		case "start":
			started = true
		}
		return "", nil
	})

	ip, err := restartProjectSidecar(context.Background(), "resume-redux", "mercury", "convex-backend")
	if err != nil {
		t.Fatalf("restartProjectSidecar: %v", err)
	}
	if ip != "192.168.66.13" {
		t.Errorf("ip = %q, want the address of the restarted container", ip)
	}
	joined := strings.Join(*calls, "\n")
	if !strings.Contains(joined, "container start cspace-resume-redux-mercury-convex-backend") {
		t.Errorf("never started the sidecar:\n%s", joined)
	}
	if strings.Contains(joined, "kill") {
		t.Errorf("escalated to kill on an already-stopped container:\n%s", joined)
	}
}

// TestRestartProjectSidecarMissingContainer — a compose sidecar's run spec
// lives in the project's compose file, which the daemon does not have, so a
// container that no longer exists cannot be recreated here. The error has to
// say what does work rather than fail obscurely.
func TestRestartProjectSidecarMissingContainer(t *testing.T) {
	calls := stubContainerCLI(t, func(args []string) (string, error) {
		if args[1] == "inspect" {
			return "[]", nil
		}
		return "", nil
	})

	_, err := restartProjectSidecar(context.Background(), "resume-redux", "mercury", "convex-backend")
	if err == nil {
		t.Fatal("restarting a container that does not exist returned nil error")
	}
	if !strings.Contains(err.Error(), "cspace up") {
		t.Errorf("error does not name the command that recreates it: %v", err)
	}
	if strings.Contains(strings.Join(*calls, "\n"), "container start") {
		t.Error("tried to start a container that does not exist")
	}
}

// TestRestartProjectSidecarEscalatesToKill covers a wedged container: the
// bounded stop does not take, so the ladder escalates rather than starting a
// container that never stopped.
func TestRestartProjectSidecarEscalatesToKill(t *testing.T) {
	killed, started := false, false
	calls := stubContainerCLI(t, func(args []string) (string, error) {
		switch args[1] {
		case "inspect":
			switch {
			case started:
				return inspectRunning, nil // back up, with an address
			case killed:
				return inspectStopped, nil // SIGKILL took
			default:
				return inspectRunning, nil // the bounded stop did not
			}
		case "kill":
			killed = true
		case "start":
			started = true
		}
		return "", nil
	})

	if _, err := restartProjectSidecar(context.Background(), "resume-redux", "mercury", "convex-backend"); err != nil {
		t.Fatalf("restartProjectSidecar: %v", err)
	}
	joined := strings.Join(*calls, "\n")
	if !strings.Contains(joined, "container kill --signal SIGKILL cspace-resume-redux-mercury-convex-backend") {
		t.Errorf("did not escalate to SIGKILL:\n%s", joined)
	}
}

// TestRestartProjectSidecarReportsStartFailure — a start that fails must not
// be reported as a successful restart just because the ladder ran.
func TestRestartProjectSidecarReportsStartFailure(t *testing.T) {
	stubContainerCLI(t, func(args []string) (string, error) {
		switch args[1] {
		case "inspect":
			return inspectStopped, nil
		case "start":
			return "no such image", errStub
		}
		return "", nil
	})

	if _, err := restartProjectSidecar(context.Background(), "resume-redux", "mercury", "convex-backend"); err == nil {
		t.Fatal("a failed start returned nil error")
	}
}

// TestResolveSidecarSandbox covers picking which sandbox's sidecar to restart
// from the host, where the sandbox is not implied by the environment the way
// it is inside one. Sidecar containers are per-sandbox
// (cspace-<project>-<sandbox>-<service>), so guessing wrong restarts someone
// else's service.
func TestResolveSidecarSandbox(t *testing.T) {
	twoSandboxes := []registry.Entry{
		{Project: "rr", Name: "mercury"},
		{Project: "rr", Name: "venus"},
	}
	oneSandbox := []registry.Entry{
		{Project: "rr", Name: "mercury"},
		{Project: "other", Name: "venus"}, // a sibling project must not count
	}

	t.Run("explicit flag wins", func(t *testing.T) {
		got, err := resolveSidecarSandbox("rr", "venus", twoSandboxes)
		if err != nil || got != "venus" {
			t.Errorf("got (%q, %v), want (venus, nil)", got, err)
		}
	})

	t.Run("single sandbox is unambiguous", func(t *testing.T) {
		got, err := resolveSidecarSandbox("rr", "", oneSandbox)
		if err != nil || got != "mercury" {
			t.Errorf("got (%q, %v), want (mercury, nil)", got, err)
		}
	})

	t.Run("several sandboxes must be disambiguated", func(t *testing.T) {
		_, err := resolveSidecarSandbox("rr", "", twoSandboxes)
		if err == nil {
			t.Fatal("ambiguous sandbox resolved silently")
		}
		// The error has to name the candidates, or the user has to go look
		// them up to answer it.
		for _, want := range []string{"mercury", "venus", "--sandbox"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err.Error(), want)
			}
		}
	})

	t.Run("no sandbox running", func(t *testing.T) {
		_, err := resolveSidecarSandbox("rr", "", []registry.Entry{{Project: "other", Name: "venus"}})
		if err == nil {
			t.Fatal("resolved a sandbox for a project that has none running")
		}
	})
}

// stubHealthyRestart models the ordinary case: the container is stopped, and
// reports running with an address once started.
func stubHealthyRestart(t *testing.T) {
	t.Helper()
	started := false
	stubContainerCLI(t, func(args []string) (string, error) {
		switch args[1] {
		case "inspect":
			if started {
				return inspectRunning, nil
			}
			return inspectStopped, nil
		case "start":
			started = true
		}
		return "", nil
	})
}

// stubSidecarPeers replaces the container listing and hosts injection seams,
// returning a recorder of every (container → hosts map) injection performed.
func stubSidecarPeers(t *testing.T, summaries []applecontainer.ContainerSummary, injectErr error) *map[string]map[string]string {
	t.Helper()
	origList, origInject := listContainersFn, injectHostsFn
	t.Cleanup(func() { listContainersFn, injectHostsFn = origList, origInject })

	injected := map[string]map[string]string{}
	listContainersFn = func(context.Context) ([]applecontainer.ContainerSummary, error) {
		return summaries, nil
	}
	injectHostsFn = func(_ context.Context, name string, hosts map[string]string) error {
		if injectErr != nil {
			return injectErr
		}
		injected[name] = hosts
		return nil
	}
	return &injected
}

// livePeers is the shape of the resume-redux sandbox during the 2026-08-28
// incident: a workspace, two sidecars, and the project's shared browser.
func livePeers() []applecontainer.ContainerSummary {
	return []applecontainer.ContainerSummary{
		{Name: "cspace-rr-mercury", State: "running", IP: "192.168.66.4"},
		{Name: "cspace-rr-mercury-convex-backend", State: "running", IP: "192.168.66.13"},
		{Name: "cspace-rr-mercury-convex-dashboard", State: "running", IP: "192.168.66.6"},
		{Name: "cspace-rr-browser", State: "running", IP: "192.168.66.3"},
		{Name: "cspace-other-venus-db", State: "running", IP: "192.168.66.9"},
	}
}

// TestRestartRefreshesPeerHosts — a restart regenerates the restarted
// container's /etc/hosts from scratch (the runtime rewrites it), so it loses
// the cspace block entirely, while peers keep pointing at the address it no
// longer has. Both were observed live: convex-backend came back with no
// injected block at all, and the dashboard still held 192.168.66.5.
func TestRestartRefreshesPeerHosts(t *testing.T) {
	stubHealthyRestart(t)
	injected := stubSidecarPeers(t, livePeers(), nil)

	if _, err := restartProjectSidecar(context.Background(), "rr", "mercury", "convex-backend"); err != nil {
		t.Fatalf("restartProjectSidecar: %v", err)
	}

	// The restarted container needs its own name back: CONVEX_SITE_ORIGIN is
	// http://convex-backend:3211, which the backend resolves against itself.
	self := (*injected)["cspace-rr-mercury-convex-backend"]
	if self["convex-backend"] != "192.168.66.13" {
		t.Errorf("restarted sidecar cannot resolve its own service name: %v", self)
	}
	// And the peer needs the new address, not the one it booted with.
	peer := (*injected)["cspace-rr-mercury-convex-dashboard"]
	if peer["convex-backend"] != "192.168.66.13" {
		t.Errorf("peer still points at a dead address: %v", peer)
	}
}

// TestRestartLeavesWorkspaceHostsAlone — the sandbox resolves sidecars through
// daemon DNS. Injecting entries there would reintroduce the exact shadowing
// bug this work removed, since files are consulted before DNS.
func TestRestartLeavesWorkspaceHostsAlone(t *testing.T) {
	stubHealthyRestart(t)
	injected := stubSidecarPeers(t, livePeers(), nil)

	if _, err := restartProjectSidecar(context.Background(), "rr", "mercury", "convex-backend"); err != nil {
		t.Fatalf("restartProjectSidecar: %v", err)
	}

	if _, ok := (*injected)["cspace-rr-mercury"]; ok {
		t.Error("wrote /etc/hosts into the workspace, shadowing DNS again")
	}
}

// TestRestartDoesNotTouchOtherSandboxes — sidecar names are per-sandbox, and a
// sibling project's containers are none of this restart's business.
func TestRestartDoesNotTouchOtherSandboxes(t *testing.T) {
	stubHealthyRestart(t)
	injected := stubSidecarPeers(t, livePeers(), nil)

	if _, err := restartProjectSidecar(context.Background(), "rr", "mercury", "convex-backend"); err != nil {
		t.Fatalf("restartProjectSidecar: %v", err)
	}

	for name := range *injected {
		if !strings.HasPrefix(name, "cspace-rr-mercury-") {
			t.Errorf("injected hosts into %q, which is not a sidecar of this sandbox", name)
		}
	}
}

// TestRestartReportsFailedHostsRefresh — a container that is running but whose
// peers cannot address it is the "looks successful but isn't" outcome this
// whole change exists to remove. It must not be reported as a clean restart.
func TestRestartReportsFailedHostsRefresh(t *testing.T) {
	stubHealthyRestart(t)
	stubSidecarPeers(t, livePeers(), errStub)

	_, err := restartProjectSidecar(context.Background(), "rr", "mercury", "convex-backend")
	if err == nil {
		t.Fatal("a failed hosts refresh was reported as a successful restart")
	}
	// The container IS up; the message has to say so, or the reader retries a
	// restart that was never the problem.
	if !strings.Contains(err.Error(), "running") {
		t.Errorf("error does not say the container came back up: %v", err)
	}
}
