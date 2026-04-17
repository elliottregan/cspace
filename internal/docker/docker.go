// Package docker provides low-level Docker CLI wrappers for operations
// not covered by the compose package (which handles docker compose commands).
package docker

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ProxyComposeProject is the compose project name used for the global
// Traefik + CoreDNS proxy stack. The proxy containers carry
// `com.docker.compose.project=<this>` labels, which EnsureProxy uses to
// distinguish compose-managed containers from orphans left by older
// cspace versions that started them via raw `docker run`.
const ProxyComposeProject = "cspace-proxy"

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
//
// The proxy compose file and Corefile are copied to ~/.cspace/proxy/ before
// starting. This ensures the bind-mounted Corefile is under $HOME, which is
// always in Docker Desktop's default shared file paths on macOS. Without this,
// Docker silently mounts an empty directory and CoreDNS crash-loops.
func EnsureProxy(assetsDir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home dir: %w", err)
	}
	proxyDir := filepath.Join(home, ".cspace", "proxy")
	if err := os.MkdirAll(proxyDir, 0755); err != nil {
		return fmt.Errorf("creating proxy dir: %w", err)
	}

	// Copy compose file and Corefile to the shared-path-safe directory.
	srcDir := filepath.Join(assetsDir, "templates", "proxy")
	for _, name := range []string{"docker-compose.yml", "Corefile"} {
		data, err := os.ReadFile(filepath.Join(srcDir, name))
		if err != nil {
			return fmt.Errorf("reading %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(proxyDir, name), data, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}

	// Remove any pre-existing proxy containers that aren't owned by this
	// compose project. Older cspace versions started them via raw `docker
	// run` (no compose labels), which causes a name-collision error when
	// the current compose stack tries to create them. Containers already
	// managed by the proxy project are left alone so compose can reuse them.
	for _, name := range []string{ProxyContainerName, "cspace-dns"} {
		removeOrphanProxyContainer(name)
	}

	composePath := filepath.Join(proxyDir, "docker-compose.yml")
	cmd := exec.Command("docker", "compose",
		"-f", composePath,
		"-p", ProxyComposeProject,
		"up", "-d",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("starting cspace proxy: %w", err)
	}
	return nil
}

// removeOrphanProxyContainer removes a container with the given name if it
// exists and is not already managed by the cspace-proxy compose project. This
// prevents conflicts when a previous cspace version left the container behind
// without compose labels (classic failure after upgrading across the era when
// the proxy stack's shape changed).
//
// Silent no-op when the container doesn't exist or is already compose-managed.
func removeOrphanProxyContainer(name string) {
	inspect := exec.Command(
		"docker", "inspect", name,
		"--format", `{{index .Config.Labels "com.docker.compose.project"}}`,
	)
	inspect.Stderr = io.Discard // quiet the "No such container" on fresh setups
	out, err := inspect.Output()
	if err != nil {
		return // container doesn't exist; compose will create it cleanly
	}
	project := strings.TrimSpace(string(out))
	if project == ProxyComposeProject {
		return // already managed correctly; compose up will reuse
	}

	if project == "" {
		fmt.Fprintf(os.Stderr, "cspace: removing orphan %q (started outside compose) so the proxy stack can recreate it\n", name)
	} else {
		fmt.Fprintf(os.Stderr, "cspace: removing %q (owned by compose project %q) so cspace-proxy can take ownership\n", name, project)
	}

	rm := exec.Command("docker", "rm", "-f", name)
	rm.Stdout = io.Discard
	rm.Stderr = io.Discard
	_ = rm.Run()
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
		// Remove old cspace.local entries and append fresh ones.
		// Uses tee instead of sed -i because /etc/hosts is a Docker bind mount
		// and sed -i fails with "resource busy" when trying to rename.
		script := fmt.Sprintf(
			`{ grep -v 'cspace\.local' /etc/hosts; echo '%s'; } | tee /etc/hosts > /dev/null`,
			hostsLine,
		)
		exec.Command("docker", "exec", id, "sh", "-c", script).Run() //nolint:errcheck
	}

	return nil
}

// Build runs `docker build` with the given options.
// image is the tag, dockerfile is the path to the Dockerfile,
// context is the build context directory, version is stamped into the
// image as the `cspace.version` label so `cspace up` can detect when a
// container image was built with an older CLI.
// If noCache is true, --no-cache is passed.
func Build(image, dockerfile, context, version string, noCache bool) error {
	args := []string{"build", "-t", image, "-f", dockerfile}
	if noCache {
		args = append(args, "--no-cache")
	}
	if version != "" {
		args = append(args, "--build-arg", "CSPACE_VERSION="+version)
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

// ImageVersion returns the cspace.version label on the given image, or ""
// if the image doesn't exist, wasn't built by cspace, or predates the
// version-stamping feature. Used by `cspace up` and the TUI to warn the
// user when the container image was built with a different CLI version.
func ImageVersion(image string) string {
	out, err := exec.Command("docker", "image", "inspect", image,
		"--format", `{{index .Config.Labels "cspace.version"}}`).Output()
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(out))
	if v == "<no value>" {
		return ""
	}
	return v
}
