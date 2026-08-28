package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
)

// Seams for the peer-hosts refresh, so the ladder is testable without a
// container runtime.
var (
	listContainersFn = func(ctx context.Context) ([]applecontainer.ContainerSummary, error) {
		return applecontainer.New().List(ctx)
	}
	injectHostsFn = InjectHosts
)

// sidecarRestartBudget bounds the whole restart ladder for one project
// sidecar. Shorter than the browser's, which also waits on CDP and a
// WebSocket handshake; here the ladder ends once the substrate reports the
// container running with an address.
const sidecarRestartBudget = 90 * time.Second

// sidecarIPWait bounds the post-start wait for an address.
const sidecarIPWait = 20 * time.Second

// projectSidecarName is the substrate-level container name for a compose
// service, matching sidecars.Orchestration.containerName.
func projectSidecarName(project, sandbox, service string) string {
	return fmt.Sprintf("cspace-%s-%s-%s", project, sandbox, service)
}

// restartProjectSidecar restarts one compose-spawned sidecar and returns the
// address it came back on. It reuses the browser sidecar's escalation ladder
// — bounded stop, SIGKILL, then host-process teardown for Apple Container's
// split-brain state (the 2026-07-17 incident) — because the failure modes are
// the substrate's, not the workload's.
//
// It restarts; it does not recreate. A compose sidecar's run spec lives in the
// project's compose file, which the daemon does not have, so a container that
// has been removed outright needs `cspace up` to rebuild it from the plan.
//
// Safe to hand to an agent because addressing no longer depends on the IP: the
// sandbox reaches sidecars through daemon DNS, which re-inspects per query, so
// coming back on a different address is invisible to callers. Before that, a
// successful restart could still strand every consumer.
func restartProjectSidecar(ctx context.Context, project, sandbox, service string) (string, error) {
	name := projectSidecarName(project, sandbox, service)

	ctx, cancel := context.WithTimeout(ctx, sidecarRestartBudget)
	defer cancel()

	// Bounded stop, best-effort: a stopped or missing container errors here
	// and the state check below decides what that means.
	_, _ = browserExecCmd(ctx, "container", "stop", "-t", "5", name)

	running, exists := containerStateRunning(ctx, name)
	if !exists {
		return "", fmt.Errorf("sidecar %s does not exist. Compose sidecars are created from the project's compose file, which only `cspace up` reads — recreate it with `cspace down %s && cspace up %s`", name, sandbox, sandbox)
	}
	if running {
		// The stop didn't take. Escalate, then fall through to start.
		_, _ = browserExecCmd(ctx, "container", "kill", "--signal", "SIGKILL", name)
		if stillRunning, _ := containerStateRunning(ctx, name); stillRunning {
			if err := resolveBrowserSplitBrain(ctx, name); err != nil {
				return "", fmt.Errorf("sidecar %s still running after SIGKILL: %w", name, err)
			}
		}
	}

	if out, err := browserExecCmd(ctx, "container", "start", name); err != nil {
		return "", fmt.Errorf("start sidecar %s: %w (%s)", name, err, strings.TrimSpace(out))
	}

	ip, err := waitForBrowserIP(ctx, name, sidecarIPWait)
	if err != nil {
		return "", fmt.Errorf("sidecar %s started but never reported an address: %w", name, err)
	}

	if err := refreshSidecarHosts(ctx, project, sandbox); err != nil {
		return "", fmt.Errorf("sidecar %s is running at %s, but refreshing sidecar /etc/hosts failed, so its peers cannot address it: %w", name, ip, err)
	}
	return ip, nil
}

// refreshSidecarHosts rewrites the cspace block in every running sidecar of
// this sandbox with the current service→IP map.
//
// Two things break without it, both observed live after the 2026-08-28
// restart. The restarted container comes back with **no** cspace block at all
// — the runtime regenerates /etc/hosts from scratch on start — so it cannot
// even resolve its own service name, which matters when its config points at
// itself (convex's CONVEX_SITE_ORIGIN is http://convex-backend:3211, and the
// backend resolves that). And every peer still holds the address the
// container had before, which is now dead.
//
// The workspace sandbox is deliberately excluded: it resolves sidecars through
// daemon DNS, and an /etc/hosts entry there would shadow it — files are
// consulted first — which is the bug this whole change removes. Sidecars get
// the file because they run their own images with no resolver of their own.
func refreshSidecarHosts(ctx context.Context, project, sandbox string) error {
	all, err := listContainersFn(ctx)
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}

	prefix := fmt.Sprintf("cspace-%s-%s-", project, sandbox)
	hosts := map[string]string{}
	var targets []string
	for _, c := range all {
		if !strings.HasPrefix(c.Name, prefix) || !strings.EqualFold(c.State, "running") {
			continue
		}
		service := strings.TrimPrefix(c.Name, prefix)
		if c.IP != "" {
			hosts[service] = c.IP
		}
		targets = append(targets, c.Name)
	}
	if len(hosts) == 0 {
		return nil
	}

	for _, target := range targets {
		if err := injectHostsFn(ctx, target, hosts); err != nil {
			return fmt.Errorf("inject hosts into %s: %w", target, err)
		}
	}
	return nil
}

// restartSidecarFn is the seam the daemon's restart handler calls, so its
// tests can fake the ladder's outcome without touching real containers.
var restartSidecarFn = restartProjectSidecar
