package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"

	v2 "github.com/elliottregan/cspace/internal/compose/v2"
)

// resolveVolume maps a compose volume to a host-bind VolumeMount under
// cspace's volume layout.
//
//   bind:   absolute or compose-dir-relative host path
//   volume: ~/.cspace/clones/<project>/<sandbox>/volumes/<name>/  (project-internal)
//   external volume: ~/.cspace/volumes/<project>/<name>/           (shared per-project)
//
// composeDir is the directory containing the compose file; relative bind
// sources resolve against it. external indicates the named volume is
// declared with external:true; externalName is the optional name override.
func resolveVolume(v v2.Volume, project, sandbox, composeDir string, external bool, externalName string) (VolumeMount, error) {
	switch v.Type {
	case "bind":
		src := v.Source
		if !filepath.IsAbs(src) {
			src = filepath.Join(composeDir, src)
		}
		return VolumeMount{HostPath: src, GuestPath: v.Target, ReadOnly: v.ReadOnly}, nil
	case "volume":
		home, err := os.UserHomeDir()
		if err != nil {
			return VolumeMount{}, err
		}
		var hp string
		if external {
			name := externalName
			if name == "" {
				name = v.Source
			}
			hp = filepath.Join(home, ".cspace", "volumes", project, name)
		} else {
			hp = filepath.Join(home, ".cspace", "clones", project, sandbox, "volumes", v.Source)
		}
		if err := os.MkdirAll(hp, 0o755); err != nil {
			return VolumeMount{}, err
		}
		return VolumeMount{HostPath: hp, GuestPath: v.Target, ReadOnly: v.ReadOnly}, nil
	default:
		return VolumeMount{}, fmt.Errorf("compose: unsupported volume type %q for %s", v.Type, v.Target)
	}
}
