// Package docker provides low-level Docker CLI wrappers for operations
// not covered by the compose package (which handles docker compose commands).
package docker

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// VolumeCreate creates a named Docker volume. Idempotent -- does not
// error if the volume already exists.
func VolumeCreate(name string) error {
	cmd := exec.Command("docker", "volume", "create", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("creating volume %s: %s: %w", name, string(out), err)
	}
	return nil
}

// VolumeRemove removes a named Docker volume.
func VolumeRemove(name string) error {
	cmd := exec.Command("docker", "volume", "rm", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("removing volume %s: %s: %w", name, string(out), err)
	}
	return nil
}

// IsContainerRunning checks whether a container with the given name exists
// and is in a running state.
func IsContainerRunning(name string) bool {
	out, err := exec.Command(
		"docker", "inspect", "-f", "{{.State.Running}}", name,
	).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// RemoveOrphanContainer removes a stopped container by name, ignoring errors
// (e.g. if the container doesn't exist). Returns an error if the container
// is currently running — callers must never silently destroy a live instance.
func RemoveOrphanContainer(name string) error {
	if IsContainerRunning(name) {
		return fmt.Errorf("container '%s' is running; refusing to remove", name)
	}
	exec.Command("docker", "rm", "-f", name).Run() //nolint:errcheck
	return nil
}

// RemoveContainer force-removes a container by name, ignoring errors
// (e.g. if the container doesn't exist). Used to clean up orphans from
// partial teardowns before creating new instances.
//
// Deprecated: Use RemoveOrphanContainer instead, which refuses to remove
// running containers.
func RemoveContainer(name string) {
	exec.Command("docker", "rm", "-f", name).Run() //nolint:errcheck
}

// Exec runs a docker command with the given arguments and returns
// the combined stdout/stderr output.
func Exec(args ...string) (string, error) {
	cmd := exec.Command("docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker %v: %s: %w", args, string(out), err)
	}
	return string(out), nil
}

// CopyToContainer copies a file from the host into a container.
// containerID can be a container name or ID.
func CopyToContainer(containerID, srcPath, dstPath string) error {
	cmd := exec.Command("docker", "cp", srcPath, containerID+":"+dstPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Build runs `docker build` with the given options.
// image is the tag, dockerfile is the path to the Dockerfile,
// context is the build context directory.
// If noCache is true, --no-cache is passed.
func Build(image, dockerfile, context string, noCache bool) error {
	args := []string{"build", "-t", image, "-f", dockerfile}
	if noCache {
		args = append(args, "--no-cache")
	}
	args = append(args, context)

	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}
	return nil
}
