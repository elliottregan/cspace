package devcontainer

import (
	"context"
	"fmt"
	"path/filepath"

	v2 "github.com/elliottregan/cspace/internal/compose/v2"
)

// Merge loads the compose file referenced by Devcontainer.DockerComposeFile
// (if set), validates cross-references between the devcontainer and the
// compose file, and returns a unified Plan.
//
// baseDir is the directory containing the devcontainer.json (compose
// file paths are resolved relative to it).
func Merge(c *Config, baseDir string) (*Plan, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	plan := &Plan{Devcontainer: c}
	if len(c.DockerComposeFile) == 0 {
		return plan, nil
	}
	composePath := filepath.Join(baseDir, c.DockerComposeFile[0])
	proj, err := v2.Parse(context.Background(), composePath)
	if err != nil {
		return nil, fmt.Errorf("compose: %w", err)
	}
	plan.Compose = proj
	plan.Service = c.Service
	if c.Service != "" {
		if _, ok := proj.Services[c.Service]; !ok {
			return nil, fmt.Errorf("devcontainer.json: service %q not found in compose file", c.Service)
		}
	}
	for _, ec := range c.Customizations.Cspace.ExtractCredentials {
		if _, ok := proj.Services[ec.From]; !ok {
			return nil, fmt.Errorf("extractCredentials: 'from' references unknown service %q", ec.From)
		}
	}
	return plan, nil
}
