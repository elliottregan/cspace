package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"

	v2 "github.com/elliottregan/cspace/internal/compose/v2"
)

// ResolvedVolume is the union returned by ResolveVolume: exactly one of
// Bind or Named is populated.
type ResolvedVolume struct {
	Bind  *VolumeMount      // host-bind mount (compose type:bind OR external named volume)
	Named *NamedVolumeMount // substrate-managed ext4 volume (compose non-external named volume)
}

// ResolveVolume maps a compose volume to either a host bind mount or a
// substrate-managed named volume.
//
//	type:bind             → host bind at the compose-dir-relative path
//	type:volume, external → host bind at ~/.cspace/volumes/<project>/<name>/
//	                        (cross-sandbox shared; only virtio-fs path that
//	                        supports multi-attach in Apple Container)
//	type:volume, internal → substrate-managed ext4 volume named
//	                        cspace-<project>-<sandbox>-<source>
//	                        (per-sandbox; no virtio-fs traffic — sidesteps
//	                        macOS kern.maxfilesperproc on cold installs)
//
// composeDir is the directory containing the compose file; relative bind
// sources resolve against it. external indicates external:true; externalName
// is the optional name override. project/sandbox scope per-sandbox volumes.
func ResolveVolume(v v2.Volume, project, sandbox, composeDir string, external bool, externalName string) (ResolvedVolume, error) {
	switch v.Type {
	case "bind":
		src := v.Source
		if !filepath.IsAbs(src) {
			src = filepath.Join(composeDir, src)
		}
		return ResolvedVolume{Bind: &VolumeMount{HostPath: src, GuestPath: v.Target, ReadOnly: v.ReadOnly}}, nil
	case "volume":
		if external {
			home, err := os.UserHomeDir()
			if err != nil {
				return ResolvedVolume{}, err
			}
			name := externalName
			if name == "" {
				name = v.Source
			}
			hp := filepath.Join(home, ".cspace", "volumes", project, name)
			if err := os.MkdirAll(hp, 0o755); err != nil {
				return ResolvedVolume{}, err
			}
			return ResolvedVolume{Bind: &VolumeMount{HostPath: hp, GuestPath: v.Target, ReadOnly: v.ReadOnly}}, nil
		}
		// Per-sandbox substrate-managed volume — ext4 disk image, no
		// virtiofs. Name is scoped so two sandboxes for the same project
		// don't clash (and so cspace down can prune cleanly).
		name := fmt.Sprintf("cspace-%s-%s-%s", project, sandbox, v.Source)
		return ResolvedVolume{Named: &NamedVolumeMount{Name: name, GuestPath: v.Target, ReadOnly: v.ReadOnly}}, nil
	default:
		return ResolvedVolume{}, fmt.Errorf("compose: unsupported volume type %q for %s", v.Type, v.Target)
	}
}
