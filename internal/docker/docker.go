// Package docker provides low-level Docker CLI wrappers for operations
// not covered by the compose package (which handles docker compose commands).
package docker

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// NetworkCreate creates a named Docker bridge network labelled for this
// project. Idempotent — if the network already exists, returns nil.
func NetworkCreate(name, instanceLabel string) error {
	out, err := exec.Command(
		"docker", "network", "inspect", name, "--format", "{{.Name}}",
	).Output()
	if err == nil && strings.TrimSpace(string(out)) == name {
		return nil
	}
	cmd := exec.Command("docker", "network", "create",
		"--label", instanceLabel,
		"--label", "cspace.network=project",
		name,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("creating network %s: %s: %w", name, out, err)
	}
	return nil
}

// NetworkRemove removes a named Docker network, ignoring errors.
// Used during teardown when no instances remain on the network.
func NetworkRemove(name string) {
	exec.Command("docker", "network", "rm", name).Run() //nolint:errcheck
}

// NetworkConnect connects a container to a network. Idempotent — returns
// nil if the container is already connected.
func NetworkConnect(network, container string) error {
	cmd := exec.Command("docker", "network", "connect", network, container)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// "already exists" means the container is already connected — success
		if strings.Contains(string(out), "already exists") {
			return nil
		}
		return fmt.Errorf("connecting %s to network %s: %s", container, network, strings.TrimSpace(string(out)))
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

// ProxyContainerName is the Docker container name for the global Traefik proxy.
const ProxyContainerName = "cspace-proxy"

// EnsureProxy starts the global Traefik + CoreDNS proxy stack if not already
// running. Runs `docker compose up -d` unconditionally because it's idempotent
// and will restart any crashed services (e.g., CoreDNS down while Traefik up).
func EnsureProxy(assetsDir string) error {
	composePath := filepath.Join(assetsDir, "templates", "proxy", "docker-compose.yml")
	cmd := exec.Command("docker", "compose",
		"-f", composePath,
		"-p", "cspace-proxy",
		"up", "-d",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("starting cspace proxy: %w", err)
	}
	return nil
}

// GetContainerIP returns a container's IP address on the given network.
func GetContainerIP(container, network string) (string, error) {
	out, err := exec.Command(
		"docker", "inspect", container,
		"--format", fmt.Sprintf(`{{(index .NetworkSettings.Networks "%s").IPAddress}}`, network),
	).Output()
	if err != nil {
		return "", fmt.Errorf("getting IP of %s on %s: %w", container, network, err)
	}
	ip := strings.TrimSpace(string(out))
	if ip == "" || ip == "<no value>" {
		return "", fmt.Errorf("%s is not connected to network %s", container, network)
	}
	return ip, nil
}

// GetTraefikHostnames discovers cspace.local hostnames from Traefik labels
// on containers in a compose project. Returns deduplicated hostnames.
func GetTraefikHostnames(composeName string) []string {
	out, err := exec.Command(
		"docker", "compose", "-p", composeName,
		"ps", "-q",
	).Output()
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var hostnames []string

	for _, id := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if id == "" {
			continue
		}
		labelOut, err := exec.Command(
			"docker", "inspect", id,
			"--format", `{{range $k, $v := .Config.Labels}}{{$k}}={{$v}}{{"\n"}}{{end}}`,
		).Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(labelOut), "\n") {
			// Match traefik.http.routers.*.rule=Host(`hostname`)
			if !strings.Contains(line, "traefik.http.routers.") || !strings.Contains(line, ".rule=") {
				continue
			}
			// Extract hostname from Host(`...`)
			start := strings.Index(line, "Host(`")
			if start < 0 {
				continue
			}
			start += len("Host(`")
			end := strings.Index(line[start:], "`)")
			if end < 0 {
				continue
			}
			hostname := line[start : start+end]
			if !seen[hostname] {
				seen[hostname] = true
				hostnames = append(hostnames, hostname)
			}
		}
	}
	return hostnames
}

// InjectHosts injects /etc/hosts entries into all containers in a compose
// stack, mapping cspace.local hostnames to Traefik's IP on the project
// network. This makes cspace.local URLs work from inside containers
// (where CoreDNS's 127.0.0.1 response doesn't reach Traefik).
func InjectHosts(composeName, projectNetwork string) error {
	proxyIP, err := GetContainerIP(ProxyContainerName, projectNetwork)
	if err != nil {
		return fmt.Errorf("resolving proxy IP: %w", err)
	}

	hostnames := GetTraefikHostnames(composeName)
	if len(hostnames) == 0 {
		return nil
	}

	hostsLine := proxyIP + " " + strings.Join(hostnames, " ")

	// Get all container IDs in the compose project
	out, err := exec.Command(
		"docker", "compose", "-p", composeName,
		"ps", "-q",
	).Output()
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}

	for _, id := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if id == "" {
			continue
		}
		// Remove old cspace.local entries and append fresh ones
		script := fmt.Sprintf(
			`sed -i '/cspace\.local/d' /etc/hosts && echo '%s' >> /etc/hosts`,
			hostsLine,
		)
		exec.Command("docker", "exec", id, "sh", "-c", script).Run() //nolint:errcheck
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
