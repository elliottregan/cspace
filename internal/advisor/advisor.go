// Package advisor manages the lifecycle of long-running advisor agents.
// See docs/superpowers/specs/2026-04-18-advisor-agents-design.md.
package advisor

import (
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"time"

	"github.com/elliottregan/cspace/internal/config"
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
		AdvisorNames:     sortedAdvisorNames(cfg),
		SystemPromptFile: "", // caller stages the system-prompt file; see Launch
		Persistent:       true,
	}, nil
}

// sortedAdvisorNames returns the configured advisor names in sorted order
// so the --advisors CLI flag is deterministic.
func sortedAdvisorNames(cfg *config.Config) []string {
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
