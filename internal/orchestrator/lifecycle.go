package orchestrator

import (
	"context"
	"fmt"
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
		if _, err := o.Substrate.Run(ctx, spec); err != nil {
			return fmt.Errorf("run sidecar %q: %w", name, err)
		}
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
