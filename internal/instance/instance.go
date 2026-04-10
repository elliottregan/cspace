// Package instance provides container lifecycle queries and command execution
// for cspace devcontainer instances.
package instance

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/elliottregan/cspace/internal/config"
	"github.com/elliottregan/cspace/internal/ports"
)

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
		return nil, nil // No instances or docker not available
	}

	seen := make(map[string]bool)
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		// Strip prefix (e.g. "mp-mercury" -> "mercury")
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
		"--filter", "label=cspace.instance=true",
		"--format", `{{.Label "com.docker.compose.project"}}	{{.Label "cspace.project"}}`,
	).Output()
	if err != nil {
		return nil, nil
	}

	seen := make(map[string]bool)
	var infos []Info
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
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
func GetInstanceDetails(cfg *config.Config) ([]Detail, error) {
	names, err := GetInstances(cfg)
	if err != nil {
		return nil, err
	}

	var details []Detail
	for _, name := range names {
		composeName := cfg.ComposeName(name)
		branch := dcExecOutput(composeName, "git", "branch", "--show-current")
		age := getContainerAge(composeName, cfg.InstanceLabel())

		details = append(details, Detail{
			Name:   name,
			Branch: branch,
			Age:    age,
		})
	}
	return details, nil
}

// GetAllInstanceDetails returns details for all cspace instances across all projects.
func GetAllInstanceDetails() ([]Detail, error) {
	infos, err := GetAllInstances()
	if err != nil {
		return nil, err
	}

	var details []Detail
	for _, info := range infos {
		branch := dcExecOutput(info.ComposeName, "git", "branch", "--show-current")
		age := getContainerAge(info.ComposeName, "cspace.instance=true")

		project := info.Project
		if project == "" {
			project = "?"
		}

		details = append(details, Detail{
			Name:    info.ComposeName,
			Project: project,
			Branch:  branch,
			Age:     age,
		})
	}
	return details, nil
}

// DcExec runs a command inside the devcontainer and returns stdout as a string.
// The command runs as the dev user in /workspace with no TTY.
func DcExec(composeName string, cmdArgs ...string) (string, error) {
	args := append([]string{
		"compose", "-p", composeName,
		"exec", "-T", "-u", "dev", "-w", "/workspace",
		"devcontainer",
	}, cmdArgs...)

	cmd := exec.Command("docker", args...)
	cmd.Stdin = nil // Non-interactive
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// DcExecInteractive runs a command inside the devcontainer with full
// stdin/stdout/stderr passthrough. Used for interactive sessions like `cspace ssh`.
func DcExecInteractive(composeName string, cmdArgs ...string) error {
	args := append([]string{
		"compose", "-p", composeName,
		"exec", "-u", "dev", "-w", "/workspace",
		"devcontainer",
	}, cmdArgs...)

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
	if len(cfg.Container.Ports) > 0 {
		for port, label := range cfg.Container.Ports {
			hostPort := ports.GetHostPort(composeName, "devcontainer", port)
			if hostPort != "" {
				fmt.Printf("  %s: http://localhost:%s\n", label, hostPort)
			}
		}
	}

	// Show any additional service ports
	out, err := exec.Command(
		"docker", "compose",
		"-p", composeName,
		"ps", "--format", "{{.Service}}",
	).Output()
	if err != nil {
		return
	}

	for _, svc := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if svc == "" || svc == "devcontainer" {
			continue
		}
		svcOut, err := exec.Command(
			"docker", "compose",
			"-p", composeName,
			"port", svc,
		).Output()
		if err != nil {
			continue
		}
		result := strings.TrimSpace(string(svcOut))
		if result == "" {
			continue
		}
		// Strip "0.0.0.0:" prefix
		if idx := strings.LastIndex(result, ":"); idx >= 0 {
			result = result[idx+1:]
		}
		fmt.Printf("  %s: http://localhost:%s\n", svc, result)
	}
}

// dcExecOutput runs a command in the devcontainer and returns stdout,
// or "?" on any error. Used internally for best-effort queries.
func dcExecOutput(composeName string, cmdArgs ...string) string {
	result, err := DcExec(composeName, cmdArgs...)
	if err != nil {
		return "?"
	}
	if result == "" {
		return "?"
	}
	return result
}

// getContainerAge queries Docker for the running time of a container
// in the given compose project.
func getContainerAge(composeName, label string) string {
	out, err := exec.Command(
		"docker", "ps",
		"--filter", "label=com.docker.compose.project="+composeName,
		"--filter", "label="+label,
		"--format", "{{.RunningFor}}",
	).Output()
	if err != nil {
		return "?"
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 0 && lines[0] != "" {
		return lines[0]
	}
	return "?"
}
