package v2

import (
	"fmt"
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
)

const docsLink = "docs/devcontainer-subset.md"

// validateSubset returns a named error for the first unsupported field
// it finds. cspace hard-rejects rather than silently mistranslating —
// see docs/superpowers/specs/2026-05-05-devcontainer-adoption-design.md.
func validateSubset(p *types.Project) error {
	if len(p.Networks) > 0 {
		var named []string
		for n := range p.Networks {
			if n != "default" {
				named = append(named, n)
			}
		}
		if len(named) > 0 {
			sort.Strings(named)
			return fmt.Errorf("compose: top-level 'networks' block has %s — cspace runs all services on the vmnet bridge with bare-name DNS; remove the block (see %s)", strings.Join(named, ", "), docsLink)
		}
	}
	for name, svc := range p.Services {
		if err := validateService(name, svc); err != nil {
			return err
		}
	}
	return nil
}

func validateService(name string, svc types.ServiceConfig) error {
	// Allow only the default network; anything beyond default is rejected
	if len(svc.Networks) > 0 {
		for netName := range svc.Networks {
			if netName != "default" {
				return fmt.Errorf("compose: service %q sets 'networks' — single default network only (see %s)", name, docsLink)
			}
		}
	}
	if svc.NetworkMode != "" {
		return fmt.Errorf("compose: service %q sets 'network_mode' — not supported (see %s)", name, docsLink)
	}
	if len(svc.CapAdd) > 0 {
		return fmt.Errorf("compose: service %q sets 'cap_add' — Apple Container does not expose Linux capability tuning (see %s)", name, docsLink)
	}
	if len(svc.CapDrop) > 0 {
		return fmt.Errorf("compose: service %q sets 'cap_drop' — not supported (see %s)", name, docsLink)
	}
	if svc.Privileged {
		return fmt.Errorf("compose: service %q sets 'privileged' — not supported (see %s)", name, docsLink)
	}
	if len(svc.Devices) > 0 {
		return fmt.Errorf("compose: service %q sets 'devices' — not supported (see %s)", name, docsLink)
	}
	if len(svc.SecurityOpt) > 0 {
		return fmt.Errorf("compose: service %q sets 'security_opt' — not supported (see %s)", name, docsLink)
	}
	if svc.Pid != "" {
		return fmt.Errorf("compose: service %q sets 'pid' — not supported (see %s)", name, docsLink)
	}
	if svc.Ipc != "" {
		return fmt.Errorf("compose: service %q sets 'ipc' — not supported (see %s)", name, docsLink)
	}
	if svc.UserNSMode != "" {
		return fmt.Errorf("compose: service %q sets 'userns_mode' — not supported (see %s)", name, docsLink)
	}
	if svc.CgroupParent != "" {
		return fmt.Errorf("compose: service %q sets 'cgroup_parent' — not supported (see %s)", name, docsLink)
	}
	if len(svc.Profiles) > 0 {
		return fmt.Errorf("compose: service %q sets 'profiles' — flatten before authoring (see %s)", name, docsLink)
	}
	if svc.Extends != nil && svc.Extends.Service != "" {
		return fmt.Errorf("compose: service %q uses 'extends' — flatten before authoring (see %s)", name, docsLink)
	}
	if len(svc.Links) > 0 {
		return fmt.Errorf("compose: service %q uses 'links' — bare service-name DNS replaces it (see %s)", name, docsLink)
	}
	return nil
}
