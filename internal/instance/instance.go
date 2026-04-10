// Package instance provides container lifecycle queries and command execution
// for cspace devcontainer instances.
package instance

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/elliottregan/cspace/internal/config"
	"github.com/elliottregan/cspace/internal/ports"
)

// GlobalInstanceLabel is the Docker label used to identify all cspace instances
// across projects. Per-project filtering uses config.InstanceLabel() instead.
const GlobalInstanceLabel = "cspace.instance=true"

// Info holds basic information about a running cspace instance.
type Info struct {
	ComposeName string // Docker Compose project name (e.g. "mp-mercury")
	Project     string // cspace project name (e.g. "myproject")
}

// Detail holds detailed information about a running instance for display.
type Detail struct {
	Name    string // Instance name (e.g. "mercury") or compose project name for --all
	Project string // Project name (only populated for cross-project queries)
	Branch  string // Current git branch inside the container
	Age     string // Human-readable container uptime
}

// GetInstances returns bare instance names for the current project.
// It queries Docker for containers matching the project's instance label,
// then strips the project prefix from compose project names.
func GetInstances(cfg *config.Config) ([]string, error) {
	prefix := cfg.Project.Prefix + "-"
	label := cfg.InstanceLabel()

	out, err := exec.Command(
		"docker", "ps",
		"--filter", "label="+label,
		"--format", `{{.Label "com.docker.compose.project"}}`,
	).Output()
	if err != nil {
		// Docker unavailable or no matching containers
		return nil, nil
	}

	seen := make(map[string]bool)
	var names []string
	for _, line := range splitLines(out) {
		name := strings.TrimPrefix(line, prefix)
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// GetAllInstances returns info about every running cspace instance
// across all projects.
func GetAllInstances() ([]Info, error) {
	out, err := exec.Command(
		"docker", "ps",
		"--filter", "label="+GlobalInstanceLabel,
		"--format", `{{.Label "com.docker.compose.project"}}	{{.Label "cspace.project"}}`,
	).Output()
	if err != nil {
		// Docker unavailable or no matching containers
		return nil, nil
	}

	seen := make(map[string]bool)
	var infos []Info
	for _, line := range splitLines(out) {
		parts := strings.SplitN(line, "\t", 2)
		composeName := parts[0]
		project := ""
		if len(parts) > 1 {
			project = parts[1]
		}
		if !seen[composeName] {
			seen[composeName] = true
			infos = append(infos, Info{
				ComposeName: composeName,
				Project:     project,
			})
		}
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].ComposeName < infos[j].ComposeName
	})
	return infos, nil
}

// IsRunning checks whether the given instance has any running containers.
func IsRunning(composeName string) bool {
	out, err := exec.Command(
		"docker", "compose",
		"-p", composeName,
		"ps", "--status", "running", "-q",
	).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// RequireRunning returns an error if the instance is not running.
func RequireRunning(composeName, name string) error {
	if !IsRunning(composeName) {
		return fmt.Errorf("instance '%s' is not running\nUse 'cspace list' to see running instances", name)
	}
	return nil
}

// GetInstanceDetails returns details for all instances of the current project.
// Branch queries run concurrently; container ages are fetched in a single
// batched docker ps call.
func GetInstanceDetails(cfg *config.Config) ([]Detail, error) {
	names, err := GetInstances(cfg)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}

	ages := getContainerAges(cfg.InstanceLabel())
	details := make([]Detail, len(names))

	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			composeName := cfg.ComposeName(name)
			details[i] = Detail{
				Name:   name,
				Branch: dcExecOutput(composeName, "git", "branch", "--show-current"),
				Age:    ages[composeName],
			}
		}(i, name)
	}
	wg.Wait()
	return details, nil
}

// GetAllInstanceDetails returns details for all cspace instances across all projects.
// Branch queries run concurrently; container ages are fetched in a single
// batched docker ps call.
func GetAllInstanceDetails() ([]Detail, error) {
	infos, err := GetAllInstances()
	if err != nil {
		return nil, err
	}
	if len(infos) == 0 {
		return nil, nil
	}

	ages := getContainerAges(GlobalInstanceLabel)
	details := make([]Detail, len(infos))

	var wg sync.WaitGroup
	for i, info := range infos {
		wg.Add(1)
		go func(i int, info Info) {
			defer wg.Done()
			project := info.Project
			if project == "" {
				project = "?"
			}
			details[i] = Detail{
				Name:    info.ComposeName,
				Project: project,
				Branch:  dcExecOutput(info.ComposeName, "git", "branch", "--show-current"),
				Age:     ages[info.ComposeName],
			}
		}(i, info)
	}
	wg.Wait()
	return details, nil
}

// DcExec runs a command inside the devcontainer and returns stdout as a string.
// The command runs as the dev user in /workspace with no TTY.
func DcExec(composeName string, cmdArgs ...string) (string, error) {
	baseArgs := []string{
		"compose", "-p", composeName,
		"exec", "-T", "-u", "dev", "-w", "/workspace",
		"devcontainer",
	}
	args := make([]string, 0, len(baseArgs)+len(cmdArgs))
	args = append(args, baseArgs...)
	args = append(args, cmdArgs...)

	cmd := exec.Command("docker", args...)
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// DcExecInteractive runs a command inside the devcontainer with full
// stdin/stdout/stderr passthrough. Used for interactive sessions like `cspace ssh`.
func DcExecInteractive(composeName string, cmdArgs ...string) error {
	baseArgs := []string{
		"compose", "-p", composeName,
		"exec", "-u", "dev", "-w", "/workspace",
		"devcontainer",
	}
	args := make([]string, 0, len(baseArgs)+len(cmdArgs))
	args = append(args, baseArgs...)
	args = append(args, cmdArgs...)

	cmd := exec.Command("docker", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// ShowPorts formats port mappings for an instance and prints to stdout.
func ShowPorts(name string, cfg *config.Config) {
	composeName := cfg.ComposeName(name)

	fmt.Printf("Ports for %s:\n", name)

	// Show devcontainer ports from config
	for port, label := range cfg.Container.Ports {
		hostPort := ports.GetHostPort(composeName, "devcontainer", port)
		if hostPort != "" {
			fmt.Printf("  %s: http://localhost:%s\n", label, hostPort)
		}
	}

	// Show any additional service ports via docker compose ps
	out, err := exec.Command(
		"docker", "compose",
		"-p", composeName,
		"ps", "--format", "{{.Service}}\t{{.Ports}}",
	).Output()
	if err != nil {
		return
	}

	for _, line := range splitLines(out) {
		parts := strings.SplitN(line, "\t", 2)
		svc := parts[0]
		if svc == "" || svc == "devcontainer" || len(parts) < 2 {
			continue
		}
		// Parse port mappings like "0.0.0.0:9222->9222/tcp"
		portStr := parts[1]
		if portStr == "" {
			continue
		}
		// Extract host port from first mapping
		if idx := strings.Index(portStr, "->"); idx >= 0 {
			hostPart := portStr[:idx]
			if colonIdx := strings.LastIndex(hostPart, ":"); colonIdx >= 0 {
				hostPort := hostPart[colonIdx+1:]
				fmt.Printf("  %s: http://localhost:%s\n", svc, hostPort)
			}
		}
	}
}

// dcExecOutput runs a command in the devcontainer and returns stdout,
// or "?" on any error.
func dcExecOutput(composeName string, cmdArgs ...string) string {
	result, err := DcExec(composeName, cmdArgs...)
	if err != nil || result == "" {
		return "?"
	}
	return result
}

// getContainerAges fetches container ages for all matching containers in a
// single docker ps call, returning a map of composeName -> age string.
func getContainerAges(label string) map[string]string {
	out, err := exec.Command(
		"docker", "ps",
		"--filter", "label="+label,
		"--format", `{{.Label "com.docker.compose.project"}}	{{.RunningFor}}`,
	).Output()
	if err != nil {
		return nil
	}

	ages := make(map[string]string)
	for _, line := range splitLines(out) {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			composeName := parts[0]
			// Only keep the first age per compose project (multiple containers)
			if _, exists := ages[composeName]; !exists {
				ages[composeName] = parts[1]
			}
		}
	}
	return ages
}

// splitLines splits command output into non-empty trimmed lines.
func splitLines(out []byte) []string {
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil
	}
	lines := strings.Split(trimmed, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}
