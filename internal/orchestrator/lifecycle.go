package orchestrator

import (
	"context"
	"fmt"
)

// Up spawns every compose service except the workspace one (Plan.Service).
// Future tasks add depends_on ordering, healthcheck waits, /etc/hosts
// injection, and credential extraction.
func (o *Orchestration) Up(ctx context.Context) error {
	if o.Plan.Compose == nil {
		return nil
	}
	for name, svc := range o.Plan.Compose.Services {
		if name == o.Plan.Service {
			continue
		}
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

// containerName produces the substrate-level name for a compose service.
// Format: cspace-<project>-<sandbox>-<service>.
func (o *Orchestration) containerName(svc string) string {
	return fmt.Sprintf("cspace-%s-%s-%s", o.Project, o.Sandbox, svc)
}
