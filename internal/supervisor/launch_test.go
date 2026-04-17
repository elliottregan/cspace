package supervisor

import (
	"strings"
	"testing"

	"github.com/elliottregan/cspace/internal/config"
)

func TestBuildSupervisorArgsIncludesResumeSession(t *testing.T) {
	cfg := &config.Config{}
	cfg.Claude.Model = "claude-opus-4-7"
	params := LaunchParams{
		Name:            "mars",
		Role:            RoleAgent,
		ResumeSessionID: "abc-123",
		StderrLog:       "/tmp/x.log",
	}
	// Avoid touching docker by passing a nonexistent compose name — the
	// per-role system-prompt probe will fail and be skipped silently.
	args := buildSupervisorArgs(params, cfg)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--resume-session abc-123") {
		t.Errorf("expected --resume-session abc-123 in args, got: %s", joined)
	}
	// When resuming, the prompt file must NOT be required.
	if strings.Contains(joined, "--prompt-file") {
		t.Errorf("expected no --prompt-file when resuming, got: %s", joined)
	}
}

func TestBuildSupervisorArgsWithoutResumeIncludesPromptFile(t *testing.T) {
	cfg := &config.Config{}
	cfg.Claude.Model = "claude-opus-4-7"
	params := LaunchParams{
		Name:       "mars",
		Role:       RoleAgent,
		PromptFile: "/tmp/p.txt",
		StderrLog:  "/tmp/x.log",
	}
	args := buildSupervisorArgs(params, cfg)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--prompt-file /tmp/p.txt") {
		t.Errorf("expected --prompt-file in args, got: %s", joined)
	}
	if strings.Contains(joined, "--resume-session") {
		t.Errorf("expected no --resume-session, got: %s", joined)
	}
}

func TestLaunchSupervisorRejectsEmptyParams(t *testing.T) {
	cfg := &config.Config{}
	cfg.Claude.Model = "claude-opus-4-7"
	params := LaunchParams{
		Name: "mars",
		Role: RoleAgent,
		// Both PromptFile and ResumeSessionID unset — should fail fast.
	}
	err := LaunchSupervisor(params, cfg)
	if err == nil {
		t.Fatal("expected error when neither PromptFile nor ResumeSessionID is set")
	}
	if !strings.Contains(err.Error(), "must be set") {
		t.Errorf("expected 'must be set' error, got: %v", err)
	}
}

func TestBuildSupervisorArgsOmitsModelWhenUnset(t *testing.T) {
	cfg := &config.Config{} // no Model set — supervisor should fall through to account default
	params := LaunchParams{
		Name:       "mars",
		Role:       RoleAgent,
		PromptFile: "/tmp/p.txt",
		StderrLog:  "/tmp/x.log",
	}
	args := buildSupervisorArgs(params, cfg)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--model") {
		t.Errorf("expected no --model flag when Model is unset, got: %s", joined)
	}
}

func TestBuildSupervisorArgsPassesModelWhenSet(t *testing.T) {
	cfg := &config.Config{}
	cfg.Claude.Model = "claude-sonnet-4-6[1m]"
	params := LaunchParams{
		Name:       "mars",
		Role:       RoleAgent,
		PromptFile: "/tmp/p.txt",
		StderrLog:  "/tmp/x.log",
	}
	args := buildSupervisorArgs(params, cfg)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--model claude-sonnet-4-6[1m]") {
		t.Errorf("expected --model claude-sonnet-4-6[1m] in args, got: %s", joined)
	}
}

func TestBuildSupervisorArgsEffortDefaultsToMaxForAutonomous(t *testing.T) {
	cfg := &config.Config{} // no Effort set
	params := LaunchParams{
		Name:       "mars",
		Role:       RoleAgent,
		PromptFile: "/tmp/p.txt",
		StderrLog:  "/tmp/x.log",
	}
	args := buildSupervisorArgs(params, cfg)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--effort max") {
		t.Errorf("expected autonomous default --effort max, got: %s", joined)
	}
}

func TestBuildSupervisorArgsPersistent(t *testing.T) {
	cfg := &config.Config{}
	params := LaunchParams{
		Name:       "venus",
		Role:       RoleAgent,
		PromptFile: "/tmp/p.txt",
		StderrLog:  "/tmp/x.log",
		Persistent: true,
	}
	args := buildSupervisorArgs(params, cfg)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--persistent") {
		t.Errorf("expected --persistent flag when LaunchParams.Persistent is true, got: %s", joined)
	}
}

func TestBuildSupervisorArgsOmitsPersistentByDefault(t *testing.T) {
	cfg := &config.Config{}
	params := LaunchParams{
		Name:       "venus",
		Role:       RoleAgent,
		PromptFile: "/tmp/p.txt",
		StderrLog:  "/tmp/x.log",
	}
	args := buildSupervisorArgs(params, cfg)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--persistent") {
		t.Errorf("expected no --persistent flag by default, got: %s", joined)
	}
}

func TestBuildSupervisorArgsEffortHonorsExplicitOverride(t *testing.T) {
	cfg := &config.Config{}
	cfg.Claude.Effort = "high"
	params := LaunchParams{
		Name:       "mars",
		Role:       RoleAgent,
		PromptFile: "/tmp/p.txt",
		StderrLog:  "/tmp/x.log",
	}
	args := buildSupervisorArgs(params, cfg)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--effort high") {
		t.Errorf("expected explicit --effort high, got: %s", joined)
	}
	if strings.Contains(joined, "--effort max") {
		t.Errorf("explicit high should not leave autonomous max fallback, got: %s", joined)
	}
}

func TestLaunchSupervisorRejectsBothSet(t *testing.T) {
	cfg := &config.Config{}
	cfg.Claude.Model = "claude-opus-4-7"
	params := LaunchParams{
		Name:            "mars",
		Role:            RoleAgent,
		PromptFile:      "/tmp/p.txt",
		ResumeSessionID: "abc-123",
	}
	err := LaunchSupervisor(params, cfg)
	if err == nil {
		t.Fatal("expected error when both PromptFile and ResumeSessionID are set")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected 'mutually exclusive' error, got: %v", err)
	}
}
