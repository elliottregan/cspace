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

// ServiceName is the Docker Compose service name for the main cspace container.
const ServiceName = "cspace"

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

// IsRunning checks whether the devcontainer service is running for the
// given instance. Only checks the devcontainer specifically — sidecars
// (playwright, chromium-cdp) being up doesn't count.
func IsRunning(composeName string) bool {
	out, err := exec.Command(
		"docker", "compose",
		"-p", composeName,
		"ps", "--status", "running", "--format", "{{.Service}}",
	).Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == ServiceName {
			return true
		}
	}
	return false
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
		ServiceName,
	}
	args := make([]string, 0, len(baseArgs)+len(cmdArgs))
	args = append(args, baseArgs...)
	args = append(args, cmdArgs...)

	cmd := exec.Command("docker", args...)
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("%s", strings.TrimSpace(string(ee.Stderr)))
		}
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
		ServiceName,
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

// PortBinding pairs a human-readable label with the host URL that maps
// to a container port.
type PortBinding struct {
	Label string
	URL   string
}

// ProbePorts queries Docker for all host port mappings associated with
// an instance and returns them as labeled URLs. Results come back in a
// stable order: configured devcontainer ports first (by config map
// iteration), then any sidecar services discovered via `docker compose
// ps`. Unavailable or unmapped ports are silently dropped; callers that
// need errors should inspect Docker directly.
func ProbePorts(name string, cfg *config.Config) []PortBinding {
	composeName := cfg.ComposeName(name)
	var bindings []PortBinding

	for port, label := range cfg.Container.Ports {
		if hostPort := ports.GetHostPort(composeName, ServiceName, port); hostPort != "" {
			bindings = append(bindings, PortBinding{
				Label: label,
				URL:   "http://localhost:" + hostPort,
			})
		}
	}

	// Sidecar services (browser, playwright run-server, etc.).
	out, err := exec.Command(
		"docker", "compose",
		"-p", composeName,
		"ps", "--format", "{{.Service}}\t{{.Ports}}",
	).Output()
	if err != nil {
		return bindings
	}
	for _, line := range splitLines(out) {
		parts := strings.SplitN(line, "\t", 2)
		svc := parts[0]
		if svc == "" || svc == ServiceName || len(parts) < 2 {
			continue
		}
		portStr := parts[1]
		if portStr == "" {
			continue
		}
		// Mapping format: "0.0.0.0:12345->9222/tcp". Take the host side
		// of the first mapping only.
		idx := strings.Index(portStr, "->")
		if idx < 0 {
			continue
		}
		hostPart := portStr[:idx]
		colonIdx := strings.LastIndex(hostPart, ":")
		if colonIdx < 0 {
			continue
		}
		bindings = append(bindings, PortBinding{
			Label: svc,
			URL:   "http://localhost:" + hostPart[colonIdx+1:],
		})
	}
	return bindings
}

// ShowPorts prints port mappings for an instance to stdout. Used by
// `cspace ports` and by the TUI's "View ports" menu entry. The
// provisioning overlay no longer calls this — it streams the same
// bindings through Reporter.Port.
func ShowPorts(name string, cfg *config.Config) {
	fmt.Printf("Ports for %s:\n", name)
	for _, b := range ProbePorts(name, cfg) {
		fmt.Printf("  %s: %s\n", b.Label, b.URL)
	}
}

// DcExecStream runs a command inside the cspace service with stdout and stderr
// streamed to the terminal. Used for long-running operations (like post-setup
// hooks) where the user needs to see progress. Returns an error if the command
// exits non-zero.
func DcExecStream(composeName string, cmdArgs ...string) error {
	baseArgs := []string{
		"compose", "-p", composeName,
		"exec", "-T", "-u", "dev", "-w", "/workspace",
		ServiceName,
	}
	args := make([]string, 0, len(baseArgs)+len(cmdArgs))
	args = append(args, baseArgs...)
	args = append(args, cmdArgs...)

	cmd := exec.Command("docker", args...)
	cmd.Stdin = nil
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// DcExecRoot runs a command inside the devcontainer as root and returns stdout.
// Unlike DcExec, this does not set -u dev or -w /workspace.
func DcExecRoot(composeName string, cmdArgs ...string) (string, error) {
	baseArgs := []string{
		"compose", "-p", composeName,
		"exec", "-T",
		ServiceName,
	}
	args := make([]string, 0, len(baseArgs)+len(cmdArgs))
	args = append(args, baseArgs...)
	args = append(args, cmdArgs...)

	cmd := exec.Command("docker", args...)
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("%s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// DcCp copies a file from the host into the cspace service.
// Equivalent to: docker compose -p <project> cp <src> cspace:<dst>
func DcCp(composeName, hostPath, containerPath string) error {
	cmd := exec.Command("docker", "compose", "-p", composeName,
		"cp", hostPath, ServiceName+":"+containerPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// SkipOnboarding pre-accepts every interactive prompt Claude Code would
// otherwise show on a fresh-container launch, so `cspace up` runs
// hands-free:
//
//  1. hasCompletedOnboarding (in ~/.claude.json) — dismisses the
//     authentication / setup wizard. Auth itself comes from
//     CLAUDE_CODE_OAUTH_TOKEN.
//  2. projects["/workspace"].hasTrustDialogAccepted (in ~/.claude.json) —
//     per-directory "Accessing workspace: /workspace" trust prompt.
//     Safe to auto-accept: /workspace is the repo we just provisioned
//     inside an isolated container.
//  3. enableAllProjectMcpServers (in /etc/claude-code/managed-settings.json) —
//     pre-approves every server a project declares in .mcp.json, skipping
//     the "New MCP server found in .mcp.json: <name>" prompt.
//  4. skipDangerousModePermissionPrompt (same file) — pre-accepts the
//     one-time "Yes, I accept" confirmation Claude shows the first time
//     it's launched with --dangerously-skip-permissions. We always launch
//     that way inside cspace, so the prompt is pure friction.
//
// Keys #3 and #4 live in the managed layer (highest precedence in Claude
// Code's settings chain) so a project's own .claude/settings.json cannot
// toggle them off. Written here, not in the Dockerfile, so policy
// changes ship with the Go CLI — no `cspace rebuild` needed.
func SkipOnboarding(composeName string) error {
	script := `const fs = require('fs'), f = '/home/dev/.claude.json';
const d = fs.existsSync(f) ? JSON.parse(fs.readFileSync(f)) : {};
d.hasCompletedOnboarding = true;
d.projects = d.projects || {};
d.projects['/workspace'] = d.projects['/workspace'] || {};
d.projects['/workspace'].hasTrustDialogAccepted = true;
fs.writeFileSync(f, JSON.stringify(d));`
	if _, err := DcExec(composeName, "node", "-e", script); err != nil {
		return err
	}
	const managedSettings = `{"enableAllProjectMcpServers":true,"skipDangerousModePermissionPrompt":true}`
	_, err := DcExecRoot(composeName, "sh", "-c",
		`mkdir -p /etc/claude-code && printf %s `+shellQuote(managedSettings)+` > /etc/claude-code/managed-settings.json`,
	)
	return err
}

// shellQuote wraps s in single quotes, escaping any embedded single
// quotes so the result is safe to embed in a POSIX shell command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
