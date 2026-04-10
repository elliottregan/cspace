// Package docker provides low-level Docker CLI wrappers for operations
// not covered by the compose package (which handles docker compose commands).
package docker

import (
	"fmt"
	"os"
	"os/exec"
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
