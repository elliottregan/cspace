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
//
// Exactly one of PromptFile or ResumeSessionID must be set. Setting both
// is a programmer error and is rejected at LaunchSupervisor entry.
type LaunchParams struct {
	Name            string // Instance name (e.g. "mercury")
	Role            string // RoleAgent or RoleCoordinator
	PromptFile      string // Container-side path to prompt file. Required unless ResumeSessionID is set.
	StderrLog       string // Container-side path for stderr log
	ResumeSessionID string // If set, supervisor resumes this session instead of starting from PromptFile.
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
	if params.PromptFile == "" && params.ResumeSessionID == "" {
		return fmt.Errorf("supervisor: either PromptFile or ResumeSessionID must be set")
	}
	if params.PromptFile != "" && params.ResumeSessionID != "" {
		return fmt.Errorf("supervisor: PromptFile and ResumeSessionID are mutually exclusive")
	}
	supervisorArgs := buildSupervisorArgs(params, cfg)

	// Redirect supervisor stderr to log file and run transcript-copy on EXIT.
	bashCmd := fmt.Sprintf(
		"trap '[ -x /workspace/.cspace/hooks/copy-transcript-on-exit.sh ] && /workspace/.cspace/hooks/copy-transcript-on-exit.sh || [ -x /opt/cspace/lib/hooks/copy-transcript-on-exit.sh ] && /opt/cspace/lib/hooks/copy-transcript-on-exit.sh' EXIT; node /opt/cspace/lib/agent-supervisor/supervisor.mjs %s 2>%s",
		shellQuoteArgs(supervisorArgs),
		params.StderrLog,
	)

	execArgs := []string{
		"exec", "-T", "-u", "dev", "-w", "/workspace",
		"-e", "CLAUDE_AUTONOMOUS=1",
		"-e", "CLAUDE_INSTANCE=" + params.Name,
		instance.ServiceName,
		"bash", "-c", bashCmd,
	}

	cmd, err := compose.Cmd(params.Name, cfg, execArgs...)
	if err != nil {
		return fmt.Errorf("building compose command: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("creating stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting supervisor: %w", err)
	}

	// Process NDJSON stream (blocks until EOF)
	ProcessStream(stdout)

	exitErr := cmd.Wait()
	exitCode := exitCodeFromError(exitErr)

	if isSuccessExit(exitCode) {
		return nil
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "FAILED \u2014 %s exited with code %d\n", params.Role, exitCode)
	fmt.Fprintf(os.Stderr, "  Shell:   cspace ssh %s\n", params.Name)
	if params.Role == RoleCoordinator {
		fmt.Fprintf(os.Stderr, "  Re-run:  cspace coordinate \"...\" --name %s (resumable)\n", params.Name)
	} else {
		fmt.Fprintf(os.Stderr, "  Re-run:  cspace up %s --prompt-file <path>\n", params.Name)
	}

	return fmt.Errorf("%s exited with code %d", params.Role, exitCode)
}

// LaunchInteractive launches Claude Code directly (not through the
// supervisor) for interactive TTY sessions.
func LaunchInteractive(name string, cfg *config.Config) error {
	cmd, err := compose.Cmd(name, cfg,
		"exec", "-u", "dev", "-w", "/workspace",
		instance.ServiceName,
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
// container. Used by RestartSupervisor after the old supervisor exits
// or the wait timeout (30s) expires.
func RelaunchDetached(params LaunchParams, cfg *config.Config, ignoreInboxBeforeMs int64) error {
	if params.PromptFile == "" && params.ResumeSessionID == "" {
		return fmt.Errorf("supervisor: either PromptFile or ResumeSessionID must be set")
	}
	if params.PromptFile != "" && params.ResumeSessionID != "" {
		return fmt.Errorf("supervisor: PromptFile and ResumeSessionID are mutually exclusive")
	}
	supervisorArgs := buildSupervisorArgs(params, cfg)

	if ignoreInboxBeforeMs > 0 {
		supervisorArgs = append(supervisorArgs, "--ignore-inbox-before", fmt.Sprintf("%d", ignoreInboxBeforeMs))
	}

	bashCmd := fmt.Sprintf(
		"node /opt/cspace/lib/agent-supervisor/supervisor.mjs %s 2>%s",
		shellQuoteArgs(supervisorArgs),
		params.StderrLog,
	)

	execArgs := []string{
		"exec", "-d", "-T", "-u", "dev", "-w", "/workspace",
		"-e", "CLAUDE_AUTONOMOUS=1",
		"-e", "CLAUDE_INSTANCE=" + params.Name,
		instance.ServiceName,
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

	logsPath := resolveLogsVolumePath(cfg)
	if logsPath == "" {
		return fmt.Errorf("cannot resolve cspace-logs volume path")
	}

	startMs := time.Now().UnixMilli()

	fmt.Printf("Interrupting supervisor for %s...\n", name)
	_ = Dispatch(composeName, "interrupt", name)

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

	// Prepend restart marker to prompt if reason given
	promptPath := ContainerPromptPath
	if reason != "" {
		safeReason := shellEscape(reason)
		restartScript := fmt.Sprintf(
			`{ echo '[This session was restarted by the coordinator. Reason: %s. Your workspace is preserved — all files, branches, and uncommitted changes are intact. Re-establish any external state (browser sessions, test servers, etc.) as needed, then continue your task.]'
echo ''
cat %s
} > %s`, safeReason, ContainerPromptPath, ContainerRestartPrompt)

		_, err := instance.DcExec(composeName, "bash", "-c", restartScript)
		if err != nil {
			return fmt.Errorf("prepending restart marker: %w", err)
		}
		promptPath = ContainerRestartPrompt
	}

	params := LaunchParams{
		Name:       name,
		Role:       RoleAgent,
		PromptFile: promptPath,
		StderrLog:  ContainerAgentStderrLog,
	}

	if err := RelaunchDetached(params, cfg, startMs); err != nil {
		return fmt.Errorf("relaunching supervisor: %w", err)
	}

	fmt.Printf("Restarted supervisor for %s (detached).\n", name)
	return nil
}

// StagePromptFile copies a host-side prompt file into the container.
func StagePromptFile(composeName, hostPath, containerPath string) error {
	if err := instance.DcCp(composeName, hostPath, containerPath); err != nil {
		return fmt.Errorf("copying prompt file: %w", err)
	}
	_, _ = instance.DcExecRoot(composeName, "chown", "dev:dev", containerPath)
	return nil
}

// StagePromptText writes inline prompt text into the container at the given path.
func StagePromptText(composeName, text, containerPath string) error {
	args := []string{
		"compose", "-p", composeName,
		"exec", "-T", "-u", "dev",
		instance.ServiceName,
		"bash", "-c", "cat > '" + shellEscape(containerPath) + "'",
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
	args := []string{"--role", params.Role}

	if params.ResumeSessionID != "" {
		args = append(args, "--resume-session", params.ResumeSessionID)
	} else {
		args = append(args, "--prompt-file", params.PromptFile)
	}

	if params.Role == RoleAgent {
		args = append(args, "--instance", params.Name)
	}

	model := cfg.Claude.Model
	if model == "" {
		model = "claude-opus-4-6"
	}
	args = append(args, "--model", model)

	if cfg.Claude.Effort != "" && cfg.Claude.Effort != "max" {
		args = append(args, "--no-effort-max")
	}

	// Check for per-role system prompt override inside the container
	systemPromptFile := filepath.Join("/workspace/.cspace/agent-supervisor",
		params.Role+"-system-prompt.txt")
	composeName := cfg.ComposeName(params.Name)
	if _, err := instance.DcExec(composeName, "test", "-f", systemPromptFile); err == nil {
		args = append(args, "--system-prompt-file", systemPromptFile)
	}

	return args
}

// resolveLogsVolumePath finds the host-side mountpoint of the cspace-logs
// Docker volume.
func resolveLogsVolumePath(cfg *config.Config) string {
	if info, err := os.Stat("/logs/messages"); err == nil && info.IsDir() {
		return "/logs/messages"
	}

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

// isSuccessExit returns true for exit codes that indicate success.
func isSuccessExit(code int) bool {
	return code == 0 || code == 2 || code == 141
}

// shellEscape escapes a string for safe inclusion in a single-quoted
// shell context by replacing each ' with '\” (end quote, escaped
// literal quote, re-open quote).
func shellEscape(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

// shellQuoteArgs wraps each argument in single quotes and joins them
// with spaces, safe for inclusion in a bash -c string.
func shellQuoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = "'" + shellEscape(a) + "'"
	}
	return strings.Join(quoted, " ")
}
