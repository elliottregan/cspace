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
}

// Mount is a host-to-container bind mount.
type Mount struct {
	HostPath      string
	ContainerPath string
	ReadOnly      bool
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
	Run(ctx context.Context, spec RunSpec) error
	Exec(ctx context.Context, name string, cmd []string, opts ExecOpts) (ExecResult, error)
	Stop(ctx context.Context, name string) error
	IP(ctx context.Context, name string) (string, error)
}
