package cli

import (
	"context"
	"errors"

	"github.com/elliottregan/cspace/internal/controlplane"
)

var _ controlplane.SandboxNamer = (*cpActor)(nil)

func (a *cpActor) SuggestName(ctx context.Context, project string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name, err := pickPlanetName(project)
	if errors.Is(err, errPlanetNamesExhausted) {
		// The dialog can accept a custom name even when there is no planet
		// left to prefill. The up command still checks actual availability.
		return "", nil
	}
	return name, err
}

func (a *cpActor) ValidateName(project, name string) error {
	return validateSandboxName(project, name)
}
