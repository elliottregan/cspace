// Package provision — teleport path. See docs/superpowers/plans/2026-04-14-teleport.md.
package provision

import (
	"fmt"

	"github.com/elliottregan/cspace/internal/config"
)

// TeleportParams holds inputs for a teleport-driven provision.
type TeleportParams struct {
	Name         string
	TeleportFrom string
	Cfg          *config.Config
}

// TeleportRun is implemented in Task 7.
func TeleportRun(p TeleportParams) error {
	return fmt.Errorf("teleport provisioning not yet implemented")
}
