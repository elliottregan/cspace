package tui

import (
	"os/exec"
	"strings"
)

// ServiceStatus represents the health of one shared cspace service.
type ServiceStatus struct {
	Name    string
	Port    string // host-bound port string e.g. ":80"; empty if not published
	Label   string // "proxy", "dns", "browser", "cdp"
	Running bool
	Slow    bool // running but degraded (reserved for future use)
}

type sharedContainerDef struct {
	name  string
	label string
	port  string
}

// sharedContainerDefs lists the global shared cspace services to health-check.
// These run as part of cspace-proxy (docker-compose.shared.yml) and the
// per-project shared sidecars.
var sharedContainerDefs = []sharedContainerDef{
	{"cspace-proxy", "proxy", ":80"},
	{"cspace-dns", "dns", ":53"},
	{"cs.playwright", "browser", ""},
	{"cs.chromium-cdp", "cdp", ":9222"},
}

// ProbeSharedServices queries Docker for the running state of each shared service.
// Called from inside a container where /var/run/docker.sock is bind-mounted.
func ProbeSharedServices() []ServiceStatus {
	return probeSharedServices()
}

func probeSharedServices() []ServiceStatus {
	out := make([]ServiceStatus, 0, len(sharedContainerDefs))
	for _, d := range sharedContainerDefs {
		raw, err := exec.Command(
			"docker", "inspect", "--format", "{{.State.Running}}", d.name,
		).Output()
		out = append(out, ServiceStatus{
			Name:    d.name,
			Port:    d.port,
			Label:   d.label,
			Running: err == nil && parseRunning(string(raw)),
		})
	}
	return out
}

// parseRunning returns true when docker inspect output is "true".
func parseRunning(output string) bool {
	return strings.TrimSpace(output) == "true"
}
