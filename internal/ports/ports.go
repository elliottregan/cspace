// Package ports handles planet-name assignment and deterministic port mapping
// for cspace instances.
//
// Planet names (mercury through neptune) get fixed, deterministic host ports.
// Custom instance names get port 0, which tells Docker to assign random ports.
package ports

import (
	"fmt"
	"os/exec"
	"strings"
)

// Planets is the ordered list of planet names available for instance assignment.
var Planets = []string{
	"mercury", "venus", "earth", "mars",
	"jupiter", "saturn", "uranus", "neptune",
}

// PortMapping holds the host port assignments for an instance.
// All instances use Docker-assigned ports (0) to avoid cross-project
// collisions. Use ports.GetHostPort() to query the actual assigned port.
type PortMapping struct {
	Dev     int // Host port for container :5173 (0 = Docker-assigned)
	Preview int // Host port for container :4173 (0 = Docker-assigned)
}

// IsPlanet returns true if the name is a known planet name.
func IsPlanet(name string) bool {
	return PlanetIndex(name) >= 0
}

// PlanetIndex returns the index of a planet name, or -1 if not found.
func PlanetIndex(name string) int {
	for i, p := range Planets {
		if p == name {
			return i
		}
	}
	return -1
}

// AssignPorts returns the port mapping for a given instance name.
// All instances get Docker-assigned ports (0) to allow the same planet
// name to run across multiple projects without port collisions.
func AssignPorts(name string) PortMapping {
	return PortMapping{Dev: 0, Preview: 0}
}

// NextPlanet returns the first planet name not currently in use by the
// current project. Names are project-scoped (compose name = prefix-planet),
// so different projects can use the same planet name simultaneously.
func NextPlanet(instanceLabel string) (string, error) {
	out, err := exec.Command(
		"docker", "ps",
		"--filter", "label="+instanceLabel,
		"--format", `{{.Label "com.docker.compose.project"}}`,
	).Output()
	if err != nil {
		// If docker fails, treat as no running instances
		out = nil
	}

	running := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			running[line] = true
		}
	}

	for _, planet := range Planets {
		inUse := false
		for name := range running {
			if name == planet || strings.HasSuffix(name, "-"+planet) {
				inUse = true
				break
			}
		}
		if !inUse {
			return planet, nil
		}
	}

	return "", fmt.Errorf("all planet names are in use; pass an explicit name")
}

// GetHostPort queries Docker for the host port mapped to a container port.
// composeName is the docker-compose project name, service is the service name,
// and port is the container port (e.g. "5173").
// Returns the host port as a string, or empty string on error.
func GetHostPort(composeName, service, port string) string {
	out, err := exec.Command(
		"docker", "compose",
		"-p", composeName,
		"port", service, port,
	).Output()
	if err != nil {
		return ""
	}
	// Output format: "0.0.0.0:12345\n" — strip the address prefix
	result := strings.TrimSpace(string(out))
	if idx := strings.LastIndex(result, ":"); idx >= 0 {
		return result[idx+1:]
	}
	return result
}
