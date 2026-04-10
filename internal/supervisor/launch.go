package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/elliottregan/cspace/internal/compose"
	"github.com/elliottregan/cspace/internal/config"
	"github.com/elliottregan/cspace/internal/instance"
)

// LaunchParams groups the arguments for launching a supervisor.
type LaunchParams struct {
	Name      string // Instance name (e.g. "mercury")
	Role      string // "agent" or "coordinator"
	PromptFile string // Container-side path to prompt file (e.g. /tmp/claude-prompt.txt)
	StderrLog string // Container-side path for stderr log (e.g. /tmp/agent-stderr.log)
}

// LaunchSupervisor runs the agent-supervisor inside the named instance
// container, reads NDJSON from its stdout, and renders status updates
// to the terminal. This is the Go equivalent of launch_supervisor() in
// supervisor.sh.
//
// Exit codes 0, 2, and 141 are treated as success:
//   - 0 = clean exit
//   - 2 = stream pipe closed
//   - 141 = SIGPIPE
func LaunchSupervisor(params LaunchParams, cfg *config.Config) error {
	// Build supervisor command args
	supervisorArgs := buildSupervisorArgs(params, cfg)

	// Build the bash command that runs inside the container:
	// - Redirect supervisor stderr to log file
	// - Run copy-transcript-on-exit.sh on EXIT (like the bash version)
	bashCmd := fmt.Sprintf(
		"trap '[ -x /workspace/.cspace/hooks/copy-transcript-on-exit.sh ] && /workspace/.cspace/hooks/copy-transcript-on-exit.sh || [ -x /opt/cspace/lib/hooks/copy-transcript-on-exit.sh ] && /opt/cspace/lib/hooks/copy-transcript-on-exit.sh' EXIT; node /opt/cspace/lib/agent-supervisor/supervisor.mjs %s 2>%s",
		strings.Join(supervisorArgs, " "),
		params.StderrLog,
	)

	// Build docker compose exec command
	execArgs := []string{
		"exec", "-T", "-u", "dev", "-w", "/workspace",
		"-e", "CLAUDE_AUTONOMOUS=1",
		"-e", "CLAUDE_INSTANCE=" + params.Name,
		"devcontainer",
		"bash", "-c", bashCmd,
	}

	cmd, err := compose.Cmd(params.Name, cfg, execArgs...)
	if err != nil {
		return fmt.Errorf("building compose command: %w", err)
	}

	// Pipe stdout for NDJSON processing
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("creating stdout pipe: %w", err)
	}

	// Inherit stderr so docker compose messages are visible
	cmd.Stderr = os.Stderr

	// Start the command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting supervisor: %w", err)
	}

	// Process NDJSON stream (blocks until EOF)
	result := ProcessStream(stdout)
	_ = result // SessionID captured for potential future use

	// Wait for command to exit
	exitErr := cmd.Wait()
	exitCode := exitCodeFromError(exitErr)

	// Exit codes 0, 2, and 141 are success
	if exitCode == 0 || exitCode == 2 || exitCode == 141 {
		return nil
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "FAILED \u2014 %s exited with code %d\n", params.Role, exitCode)
	fmt.Fprintf(os.Stderr, "  Shell:   cspace ssh %s\n", params.Name)
	if params.Role == "coordinator" {
		fmt.Fprintf(os.Stderr, "  Re-run:  cspace coordinate \"...\" --name %s (resumable)\n", params.Name)
	} else {
		fmt.Fprintf(os.Stderr, "  Re-run:  cspace up %s --prompt-file <path>\n", params.Name)
	}

	return fmt.Errorf("%s exited with code %d", params.Role, exitCode)
}

// LaunchInteractive launches Claude Code directly (not through the
// supervisor) for interactive TTY sessions. Equivalent to:
//
//	exec docker compose exec -u dev -w /workspace devcontainer claude --dangerously-skip-permissions
func LaunchInteractive(name string, cfg *config.Config) error {
	cmd, err := compose.Cmd(name, cfg,
		"exec", "-u", "dev", "-w", "/workspace",
		"devcontainer",
		"claude", "--dangerously-skip-permissions",
	)
	if err != nil {
		return fmt.Errorf("building compose command: %w", err)
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// RelaunchDetached launches a supervisor in detached mode inside the
// container. Used by RestartSupervisor after the old supervisor exits.
// The supervisor runs in the background with no terminal rendering.
func RelaunchDetached(params LaunchParams, cfg *config.Config, ignoreInboxBeforeMs int64) error {
	supervisorArgs := buildSupervisorArgs(params, cfg)

	if ignoreInboxBeforeMs > 0 {
		supervisorArgs = append(supervisorArgs, "--ignore-inbox-before", fmt.Sprintf("%d", ignoreInboxBeforeMs))
	}

	bashCmd := fmt.Sprintf(
		"node /opt/cspace/lib/agent-supervisor/supervisor.mjs %s 2>%s",
		strings.Join(supervisorArgs, " "),
		params.StderrLog,
	)

	// Use -d (detached) and -T (no TTY)
	execArgs := []string{
		"exec", "-d", "-T", "-u", "dev", "-w", "/workspace",
		"-e", "CLAUDE_AUTONOMOUS=1",
		"-e", "CLAUDE_INSTANCE=" + params.Name,
		"devcontainer",
		"bash", "-c", bashCmd,
	}

	cmd, err := compose.Cmd(params.Name, cfg, execArgs...)
	if err != nil {
		return fmt.Errorf("building compose command: %w", err)
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// RestartSupervisor restarts an agent's supervisor inside its existing
// container. Sends an interrupt to the old supervisor, waits for it to
// exit cleanly, then launches a new supervisor with the same prompt.
func RestartSupervisor(name, reason string, cfg *config.Config) error {
	composeName := cfg.ComposeName(name)

	// Find the logs volume path for completion polling
	logsPath := resolveLogsVolumePath(cfg)
	if logsPath == "" {
		return fmt.Errorf("cannot resolve cspace-logs volume path")
	}

	startMs := time.Now().UnixMilli()

	// Interrupt the old supervisor
	fmt.Printf("Interrupting supervisor for %s...\n", name)
	Dispatch(composeName, "interrupt", name)

	// Wait for completion notification (up to 30s)
	completionDir := filepath.Join(logsPath, "_coordinator", "inbox")
	completionPattern := fmt.Sprintf("completion-%s-*", name)

	found := false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(completionDir)
		if err == nil {
			for _, e := range entries {
				matched, _ := filepath.Match(completionPattern, e.Name())
				if !matched {
					continue
				}
				info, err := e.Info()
				if err != nil {
					continue
				}
				// Check if the file is newer than our start time
				if info.ModTime().UnixMilli() >= startMs {
					found = true
					break
				}
			}
		}
		if found {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	if !found {
		fmt.Fprintln(os.Stderr, "WARNING: timed out waiting for old supervisor to exit (30s)")
	} else {
		fmt.Println("Old supervisor exited cleanly.")
	}

	// If reason given, prepend a restart marker to the prompt
	promptPath := "/tmp/claude-prompt.txt"
	if reason != "" {
		restartScript := fmt.Sprintf(
			`{ echo '[This session was restarted by the coordinator. Reason: %s. Your workspace is preserved — all files, branches, and uncommitted changes are intact. Re-establish any external state (browser sessions, test servers, etc.) as needed, then continue your task.]'
echo ''
cat /tmp/claude-prompt.txt
} > /tmp/restart-prompt.txt`, reason)

		_, err := instance.DcExec(composeName, "bash", "-c", restartScript)
		if err != nil {
			return fmt.Errorf("prepending restart marker: %w", err)
		}
		promptPath = "/tmp/restart-prompt.txt"
	}

	// Build effort and system prompt flags
	params := LaunchParams{
		Name:      name,
		Role:      "agent",
		PromptFile: promptPath,
		StderrLog: "/tmp/agent-stderr.log",
	}

	// Launch new supervisor detached with inbox filter
	if err := RelaunchDetached(params, cfg, startMs); err != nil {
		return fmt.Errorf("relaunching supervisor: %w", err)
	}

	fmt.Printf("Restarted supervisor for %s (detached).\n", name)
	return nil
}

// StagePromptFile copies a host-side prompt file into the container at
// the given container path. Also fixes ownership to the dev user.
func StagePromptFile(composeName, hostPath, containerPath string) error {
	if err := instance.DcCp(composeName, hostPath, containerPath); err != nil {
		return fmt.Errorf("copying prompt file: %w", err)
	}
	// Fix ownership
	instance.DcExecRoot(composeName, "chown", "dev:dev", containerPath)
	return nil
}

// StagePromptText writes inline prompt text into the container at the given path.
func StagePromptText(composeName, text, containerPath string) error {
	// Use bash -c 'cat > path' with text on stdin
	args := []string{
		"compose", "-p", composeName,
		"exec", "-T", "-u", "dev",
		"devcontainer",
		"bash", "-c", "cat > " + containerPath,
	}

	cmd := exec.Command("docker", args...)
	cmd.Stdin = strings.NewReader(text)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("staging prompt text: %s: %w", string(out), err)
	}
	return nil
}

// buildSupervisorArgs constructs the command-line arguments for supervisor.mjs.
func buildSupervisorArgs(params LaunchParams, cfg *config.Config) []string {
	args := []string{
		"--role", params.Role,
		"--prompt-file", params.PromptFile,
	}

	// Only the agent role takes --instance
	if params.Role == "agent" {
		args = append(args, "--instance", params.Name)
	}

	// Model
	model := cfg.Claude.Model
	if model == "" {
		model = "claude-opus-4-6"
	}
	args = append(args, "--model", model)

	// Effort flag: pass --no-effort-max when effort is set and not "max"
	if cfg.Claude.Effort != "" && cfg.Claude.Effort != "max" {
		args = append(args, "--no-effort-max")
	}

	// System prompt override: check for per-role override file in the container
	// This is done by checking via dc_exec, but for simplicity we just pass the
	// flag if the file exists. Since we can't check the container filesystem
	// synchronously here without another docker exec, we'll check via a known
	// convention: if the project has .cspace/agent-supervisor/<role>-system-prompt.txt
	systemPromptFile := filepath.Join("/workspace/.cspace/agent-supervisor",
		params.Role+"-system-prompt.txt")
	composeName := cfg.ComposeName(params.Name)
	if _, err := instance.DcExec(composeName, "test", "-f", systemPromptFile); err == nil {
		args = append(args, "--system-prompt-file", systemPromptFile)
	}

	return args
}

// resolveLogsVolumePath finds the host-side mountpoint of the cspace-logs
// Docker volume so host-side scripts can read /logs/messages/.
func resolveLogsVolumePath(cfg *config.Config) string {
	// If we're inside a container, use the direct path
	if info, err := os.Stat("/logs/messages"); err == nil && info.IsDir() {
		return "/logs/messages"
	}

	// Try to inspect the Docker volume
	vol := cfg.LogsVolume()
	out, err := exec.Command("docker", "volume", "inspect", vol,
		"--format", "{{ .Mountpoint }}").Output()
	if err != nil {
		return ""
	}

	mp := strings.TrimSpace(string(out))
	if mp != "" {
		if info, err := os.Stat(mp); err == nil && info.IsDir() {
			return mp + "/messages"
		}
	}

	return ""
}

// exitCodeFromError extracts the exit code from an exec error.
// Returns 0 for nil errors.
func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			return status.ExitStatus()
		}
	}
	return 1
}
