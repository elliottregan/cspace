// Package substrate is the cspace runtime abstraction for OCI-compatible
// sandbox backends. Adapters live in subpackages (applecontainer,
// containerd, ...). The interface is intentionally minimal during P0;
// it grows in P1 as more capabilities are needed.
package substrate

import "context"

// RunSpec is everything an adapter needs to start a sandbox.
type RunSpec struct {
	Name        string            // unique container name
	Image       string            // OCI image reference
	Command     []string          // entrypoint override (empty = use image default)
	Env         map[string]string // environment variables
	Mounts      []Mount           // host-to-container bind mounts
	PublishPort []PortMap         // ports to publish on the host
	DNS         []string          // resolvers to inject; empty = adapter default
	// Resource caps. Zero means "let the adapter pick a default" — the
	// adapter is free to apply its own sensible default rather than hand
	// off to the underlying CLI's default, which on Apple Container is a
	// too-tight 1024 MiB / 4 CPU.
	CPUs      int // number of CPUs to allocate; 0 = adapter default
	MemoryMiB int // memory cap in MiB;            0 = adapter default

	// RuntimeOverlayPath is the host-side path to ~/.cspace/runtime/<version>/.
	// When non-empty, the adapter bind-mounts it read-only at /opt/cspace
	// inside the microVM. This decouples cspace runtime upgrades (scripts,
	// supervisor, plugin-install machinery) from project image rebuilds.
	RuntimeOverlayPath string

	// TmpfsMounts request RAM-backed in-microVM filesystems. Use cases:
	// build artifacts (node_modules, .next, target/) that should NOT
	// pollute the host disk, and that need to bypass virtio-fs's per-mount
	// fd budget (which a 1700-package pnpm install saturates). Lost on
	// container restart by design.
	TmpfsMounts []TmpfsMount

	// Volumes attach substrate-managed (NOT bind-mounted) volumes. The
	// adapter creates them on demand, mounts them as in-VM block devices
	// (e.g. /dev/vdc ext4) — no host virtio-fs traffic on read/write
	// paths. This is the v0-Docker-Desktop equivalent: host's
	// kern.maxfilesperproc never sees a 1700-pkg pnpm install. Compose
	// non-external named volumes resolve here.
	Volumes []NamedVolume
}

// Mount is a host-to-container bind mount.
type Mount struct {
	HostPath      string
	ContainerPath string
	ReadOnly      bool
}

// NamedVolume is a substrate-managed block-device volume attached to the
// microVM. Apple Container backs these with an ext4-formatted disk image
// at ~/Library/Application Support/com.apple.container/volumes/<name>/
// volume.img. Reads/writes never traverse virtio-fs, so cold pnpm install
// extracts don't saturate the host's kern.maxfilesperproc.
//
// Volumes are exclusive: a single volume can only attach to one running
// container at a time, so this is the right shape for per-sandbox
// node_modules but NOT for cross-sandbox shared caches.
type NamedVolume struct {
	// Name is the substrate-level volume name. Caller is responsible for
	// scoping (e.g. cspace-<project>-<sandbox>-<compose-vol>).
	Name string
	// ContainerPath where the volume mounts inside the microVM.
	ContainerPath string
	// SizeBytes is the ext4 image size; 0 = adapter default.
	SizeBytes int64
	// ReadOnly mounts the volume read-only.
	ReadOnly bool
}

// TmpfsMount is a RAM-backed mount inside the microVM.
type TmpfsMount struct {
	// ContainerPath where the tmpfs is mounted (e.g., "/workspace/node_modules").
	ContainerPath string
	// SizeMiB caps the tmpfs at this many MiB. 0 = adapter default
	// (typically half of the microVM's RAM).
	SizeMiB int
}

// PortMap publishes a container port on the host.
type PortMap struct {
	HostPort      int
	ContainerPort int
}

// ExecOpts tunes a single Exec invocation.
type ExecOpts struct {
	WorkDir string
	Env     map[string]string
	// User selects the in-container user. Empty means "inherit the
	// image's USER". Set "0" or "root" for orchestrator-side execs
	// that must write privileged files (e.g. /etc/hosts injection)
	// in sidecar images that ship a non-root USER.
	User string
}

// ExecResult captures the outcome of an Exec call. ExitCode is 0 on success,
// non-zero on a clean exit with that status. Transport-level errors are
// returned as the error value of Exec, not encoded here.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Substrate is the minimal P0 surface for a sandbox runtime.
type Substrate interface {
	Available() bool
	// HealthCheck verifies the substrate is operational and ready to accept
	// Run/Exec/Stop calls. Returns a clear, user-actionable error when not.
	HealthCheck(ctx context.Context) error
	Run(ctx context.Context, spec RunSpec) error
	Exec(ctx context.Context, name string, cmd []string, opts ExecOpts) (ExecResult, error)
	Stop(ctx context.Context, name string) error
	IP(ctx context.Context, name string) (string, error)
	// Version returns the raw output of the substrate's CLI version probe
	// (e.g. `container --version`). Adapters define their own version-range
	// policy via separate helpers — this method returns the raw string only.
	Version(ctx context.Context) (string, error)
}
