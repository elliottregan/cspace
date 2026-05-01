// Package sandboxmode detects whether the running cspace process is
// executing inside a sandbox. When in-sandbox, the binary should skip
// project-root / git-repo discovery and read its context from env vars
// injected by the host's cspace up command.
package sandboxmode

import "os"

// IsInSandbox returns true when the process is running inside a cspace sandbox.
// Detection is based on CSPACE_SANDBOX_NAME (always injected by cspace up).
func IsInSandbox() bool {
	return os.Getenv("CSPACE_SANDBOX_NAME") != ""
}

// Project returns the project name set by the host at sandbox-create time.
// Empty when not in a sandbox.
func Project() string {
	return os.Getenv("CSPACE_PROJECT")
}

// Name returns the sandbox's own name. Empty when not in a sandbox.
func Name() string {
	return os.Getenv("CSPACE_SANDBOX_NAME")
}

// RegistryURL returns the host registry-daemon URL injected at sandbox-create.
// Empty when not in a sandbox.
func RegistryURL() string {
	return os.Getenv("CSPACE_REGISTRY_URL")
}
