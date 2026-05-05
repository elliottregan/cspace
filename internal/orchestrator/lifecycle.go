package orchestrator

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	v2 "github.com/elliottregan/cspace/internal/compose/v2"
)

// Up spawns every compose service except the workspace one (Plan.Service).
// Services are spawned in topological order respecting depends_on.
// Future tasks add healthcheck waits, /etc/hosts injection, and credential extraction.
func (o *Orchestration) Up(ctx context.Context) error {
	if o.Plan.Compose == nil {
		return nil
	}
	order, err := topoSort(o.Plan.Compose.Services)
	if err != nil {
		return err
	}
	for _, name := range order {
		if name == o.Plan.Service {
			continue
		}
		svc := o.Plan.Compose.Services[name]
		spec := ServiceSpec{
			Name:        o.containerName(name),
			Image:       svc.Image,
			Environment: svc.Environment,
			Command:     svc.Command,
			WorkingDir:  svc.WorkingDir,
			User:        svc.User,
		}
		composeDir := filepath.Dir(o.Plan.Compose.SourcePath)
		for _, v := range svc.Volumes {
			external := false
			externalName := ""
			if nv, ok := o.Plan.Compose.NamedVolumes[v.Source]; ok {
				external = nv.External
				externalName = nv.Name
			}
			vm, err := resolveVolume(v, o.Project, o.Sandbox, composeDir, external, externalName)
			if err != nil {
				return fmt.Errorf("resolve volume for service %q: %w", name, err)
			}
			spec.Volumes = append(spec.Volumes, vm)
		}
		if _, err := o.Substrate.Run(ctx, spec); err != nil {
			return fmt.Errorf("run sidecar %q: %w", name, err)
		}
		if needsHealthy(o.Plan.Compose, name) && svc.Healthcheck != nil {
			containerName := o.containerName(name)
			adapter := func(ctx context.Context, cmd []string) (string, int, error) {
				out, err := o.Substrate.Exec(ctx, containerName, cmd)
				if err != nil {
					return out, 1, err
				}
				return out, 0, nil
			}
			if err := waitHealthy(ctx, svc.Healthcheck, adapter); err != nil {
				return fmt.Errorf("healthcheck for %q: %w", name, err)
			}
		}
	}
	// Build IP map: every compose service (sandbox + sidecars) by its
	// compose-declared name. The workspace's sandbox IP comes via the
	// substrate; sidecar IPs were captured by Substrate.Run.
	if err := o.injectAllHosts(ctx); err != nil {
		return err
	}
	if err := o.ExtractAll(ctx); err != nil {
		return err
	}
	return nil
}

// topoSort returns service names in spawn order: each service appears
// after all of its depends_on targets. Returns a "cycle" error if the
// graph is cyclic.
func topoSort(services map[string]*v2.Service) ([]string, error) {
	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := map[string]int{}
	var order []string
	var visit func(string) error
	visit = func(n string) error {
		switch state[n] {
		case visiting:
			return fmt.Errorf("compose: dependency cycle through %q", n)
		case done:
			return nil
		}
		state[n] = visiting
		if svc, ok := services[n]; ok && svc != nil {
			for _, d := range svc.DependsOn {
				if _, ok := services[d.Name]; !ok {
					continue
				}
				if err := visit(d.Name); err != nil {
					return err
				}
			}
		}
		state[n] = done
		order = append(order, n)
		return nil
	}
	names := make([]string, 0, len(services))
	for n := range services {
		names = append(names, n)
	}
	sort.Strings(names) // deterministic visit order across runs
	for _, n := range names {
		if err := visit(n); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// containerName produces the substrate-level name for a compose service.
// Format: cspace-<project>-<sandbox>-<service>.
func (o *Orchestration) containerName(svc string) string {
	return fmt.Sprintf("cspace-%s-%s-%s", o.Project, o.Sandbox, svc)
}

// needsHealthy reports whether any other service depends on `name`
// with the service_healthy condition.
func needsHealthy(p *v2.Project, name string) bool {
	for _, s := range p.Services {
		for _, d := range s.DependsOn {
			if d.Name == name && d.Condition == "service_healthy" {
				return true
			}
		}
	}
	return false
}

func (o *Orchestration) injectAllHosts(ctx context.Context) error {
	if o.Plan.Compose == nil {
		return nil
	}
	ips := map[string]string{}
	for name := range o.Plan.Compose.Services {
		var target string
		if name == o.Plan.Service {
			target = o.Sandbox
		} else {
			target = o.containerName(name)
		}
		ip, err := o.Substrate.IP(ctx, target)
		if err != nil {
			return fmt.Errorf("ip lookup for %q: %w", name, err)
		}
		ips[name] = ip
	}
	content := renderHosts(ips)
	for name := range o.Plan.Compose.Services {
		var target string
		if name == o.Plan.Service {
			target = o.Sandbox
		} else {
			target = o.containerName(name)
		}
		if err := injectHosts(ctx, o.Substrate, target, content); err != nil {
			return err
		}
	}
	return nil
}
