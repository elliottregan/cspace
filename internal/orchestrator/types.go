// Package orchestrator manages the lifecycle of compose-defined sidecar
// services running as Apple Container microVMs alongside the main
// sandbox. It handles spawn ordering (depends_on), healthcheck waits,
// /etc/hosts injection for bare-name DNS, credential extraction, and
// teardown.
package orchestrator

import (
	"context"

	"github.com/elliottregan/cspace/internal/devcontainer"
)

// Substrate is the minimal surface the orchestrator needs from the
// substrate adapter — separated for testability so unit tests can use
// a stub instead of Apple Container.
type Substrate interface {
	Run(ctx context.Context, spec ServiceSpec) (string, error) // returns IP
	Exec(ctx context.Context, name string, cmd []string) (stdout string, err error)
	Stop(ctx context.Context, name string) error
	IP(ctx context.Context, name string) (string, error)
}

type ServiceSpec struct {
	Name         string
	Image        string
	Environment  map[string]string
	Volumes      []VolumeMount      // host bind mounts (compose type:bind, external named volumes)
	NamedVolumes []NamedVolumeMount // substrate-managed ext4 volumes (compose non-external named volumes)
	Tmpfs        []TmpfsMount
	Command      []string
	WorkingDir   string
	User         string
}

type VolumeMount struct {
	HostPath  string
	GuestPath string
	ReadOnly  bool
}

// NamedVolumeMount is a substrate-managed (ext4 disk image, not virtio-fs)
// volume. Used for per-sandbox node_modules, build artifacts, anything
// where the host shouldn't see the I/O. Cspace names these
// cspace-<project>-<sandbox>-<compose-volume>.
type NamedVolumeMount struct {
	Name      string // substrate-level volume name
	GuestPath string
	ReadOnly  bool
}

type TmpfsMount struct {
	GuestPath string
	SizeMiB   int
}

// Orchestration coordinates one sandbox + its compose-declared sidecars.
type Orchestration struct {
	Sandbox   string // sandbox container name, e.g. "mercury"
	Project   string // project name, e.g. "resume-redux"
	Plan      *devcontainer.Plan
	Substrate Substrate
	// ExtractedEnv is populated by Up after credential extraction runs;
	// cmd_up consumes this to inject env vars into the sandbox.
	ExtractedEnv map[string]string
	// serviceIPs is populated by injectAllHosts; keyed by compose service
	// name → vmnet IP. Exposed via ServiceIPs() so cmd_up can extend hosts
	// injection into cspace-private microVMs (e.g. the browser sidecar).
	serviceIPs map[string]string
}

// ServiceIPs returns a copy of the service-name → IP map captured during
// Up's hosts injection. Empty before Up has run, or when the project has
// no compose sidecars.
func (o *Orchestration) ServiceIPs() map[string]string {
	if o.serviceIPs == nil {
		return nil
	}
	out := make(map[string]string, len(o.serviceIPs))
	for k, v := range o.serviceIPs {
		out[k] = v
	}
	return out
}
