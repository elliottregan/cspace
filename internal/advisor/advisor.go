// Package advisor manages the lifecycle of long-running advisor agents.
// See docs/superpowers/specs/2026-04-18-advisor-agents-design.md.
package advisor

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/elliottregan/cspace/internal/compose"
	"github.com/elliottregan/cspace/internal/config"
	"github.com/elliottregan/cspace/internal/instance"
	"github.com/elliottregan/cspace/internal/provision"
	"github.com/elliottregan/cspace/internal/supervisor"
)

// BuildLaunchParams assembles the LaunchParams for a configured advisor.
// Returns an error if the advisor is not in cfg.Advisors.
func BuildLaunchParams(cfg *config.Config, name string) (supervisor.LaunchParams, error) {
	spec, ok := cfg.Advisors[name]
	if !ok {
		return supervisor.LaunchParams{}, fmt.Errorf("advisor %q not configured", name)
	}

	return supervisor.LaunchParams{
		Name:             name,
		Role:             supervisor.RoleAdvisor,
		ModelOverride:    spec.Model,
		EffortOverride:   spec.Effort,
		AdvisorNames:     SortedAdvisorNames(cfg),
		SystemPromptFile: "", // caller stages the system-prompt file; see Launch
		Persistent:       true,
	}, nil
}

// SortedAdvisorNames returns the configured advisor names in sorted order
// so the --advisors CLI flag is deterministic.
func SortedAdvisorNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Advisors))
	for n := range cfg.Advisors {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// IsAlive returns true when the advisor's supervisor socket answers a
// status request. Reused from the coordinator's liveness probe pattern.
func IsAlive(cfg *config.Config, name string) bool {
	logsPath := supervisor.ResolveLogsVolumePath(cfg)
	if logsPath == "" {
		return false
	}
	sockPath := filepath.Join(logsPath, name, "supervisor.sock")
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	req, _ := json.Marshal(map[string]string{"cmd": "status"})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return false
	}
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return false
	}
	var reply struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(buf[:n], &reply); err != nil {
		return false
	}
	return reply.OK
}

// Launch brings the named advisor up if not already alive. It:
//  1. provisions the container if missing
//  2. checks out the configured baseBranch
//  3. stages the system prompt and bootstrap prompt
//  4. launches the supervisor detached (role=advisor, persistent)
//
// Returns reused=true when the advisor was already alive (no-op), reused=false
// when a fresh provision+launch was initiated. A fresh launch is detached
// and asynchronous — the supervisor socket may not be ready immediately
// after Launch returns.
func Launch(cfg *config.Config, name string) (reused bool, err error) {
	spec, ok := cfg.Advisors[name]
	if !ok {
		return false, fmt.Errorf("advisor %q not configured", name)
	}

	if IsAlive(cfg, name) {
		return true, nil
	}

	if _, err := provision.Run(provision.Params{Name: name, Cfg: cfg}); err != nil {
		return false, fmt.Errorf("provisioning advisor %s: %w", name, err)
	}

	composeName := cfg.ComposeName(name)
	_ = instance.SkipOnboarding(composeName)

	// Re-copy host .env so the advisor inherits GH_TOKEN, etc. (matches coordinator behavior).
	envFile := filepath.Join(cfg.ProjectRoot, ".env")
	if _, err := os.Stat(envFile); err == nil {
		_ = instance.DcCp(composeName, envFile, "/workspace/.env")
		_, _ = instance.DcExecRoot(composeName, "chown", "dev:dev", "/workspace/.env")
	}

	// Check out the configured baseBranch (default main) inside the advisor's workspace.
	// The advisor can switch branches itself later if a consultation requires it.
	baseBranch := spec.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}
	// git fetch is best-effort; container may be offline. checkout must succeed.
	_, _ = instance.DcExec(composeName, "git", "-C", "/workspace", "fetch", "origin")
	if _, err := instance.DcExec(composeName, "git", "-C", "/workspace", "checkout", baseBranch); err != nil {
		return false, fmt.Errorf("checking out advisor baseBranch %s: %w", baseBranch, err)
	}

	// Stage the system prompt (ResolveAdvisor handles override/fallback).
	systemPromptHost := cfg.ResolveAdvisor(name)
	if systemPromptHost == "" {
		return false, fmt.Errorf("no system prompt resolved for advisor %s", name)
	}
	const containerSystemPromptPath = "/tmp/advisor-system-prompt.txt"
	if err := supervisor.StagePromptFile(composeName, systemPromptHost, containerSystemPromptPath); err != nil {
		return false, fmt.Errorf("staging advisor system prompt: %w", err)
	}

	// Render and stage the bootstrap prompt.
	bootstrap := renderBootstrapPrompt(name)
	const containerBootstrapPath = "/tmp/advisor-bootstrap.txt"
	if err := supervisor.StagePromptText(composeName, bootstrap, containerBootstrapPath); err != nil {
		return false, fmt.Errorf("staging advisor bootstrap prompt: %w", err)
	}

	params, err := BuildLaunchParams(cfg, name)
	if err != nil {
		return false, err
	}
	params.PromptFile = containerBootstrapPath
	params.StderrLog = supervisor.ContainerAgentStderrLog
	params.SystemPromptFile = containerSystemPromptPath

	// Detached launch — the coordinator does not block on advisor stdout.
	if err := supervisor.RelaunchDetached(params, cfg, 0); err != nil {
		return false, err
	}
	return false, nil
}

func renderBootstrapPrompt(name string) string {
	return fmt.Sprintf(`You are the %s advisor. Your role is defined in your system prompt
(already applied to this session).

Project principles, direction, and decisions live in the cspace-context
server — call read_context at the start of each consultation for current
values.

You will receive messages via the agent-messenger MCP tools. Reply via
reply_to_coordinator / reply_to_worker. See your system prompt for
response format and quality bar.

Do a light read of read_context(["direction","principles","roadmap"])
now so you have baseline context. Then wait for messages.`, name)
}

// Teardown shuts down the advisor's supervisor and stops its container.
// Session state is lost.
func Teardown(cfg *config.Config, name string) error {
	if _, ok := cfg.Advisors[name]; !ok {
		return fmt.Errorf("advisor %q not configured", name)
	}
	composeName := cfg.ComposeName(name)

	// Best-effort interrupt the supervisor (closes prompt queue cleanly).
	_ = supervisor.Dispatch(composeName, "interrupt", name)

	// Stop the container and remove volumes — same path as `cspace down`.
	if err := compose.Run(name, cfg, "down", "--volumes"); err != nil {
		return fmt.Errorf("stopping advisor container: %w", err)
	}
	return nil
}
