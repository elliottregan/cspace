package supervisor

import (
	"strings"
	"testing"

	"github.com/elliottregan/cspace/internal/config"
)

func TestBuildSupervisorArgsIncludesResumeSession(t *testing.T) {
	cfg := &config.Config{}
	cfg.Claude.Model = "claude-opus-4-6"
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
	cfg.Claude.Model = "claude-opus-4-6"
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
	cfg.Claude.Model = "claude-opus-4-6"
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

func TestBuildSupervisorArgsModelDefaultsWhenUnset(t *testing.T) {
	cfg := &config.Config{} // no Model set
	params := LaunchParams{
		Name:       "mars",
		Role:       RoleAgent,
		PromptFile: "/tmp/p.txt",
		StderrLog:  "/tmp/x.log",
	}
	args := buildSupervisorArgs(params, cfg)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--model claude-opus-4-6") {
		t.Errorf("expected default model claude-opus-4-6 in args, got: %s", joined)
	}
}

func TestLaunchSupervisorRejectsBothSet(t *testing.T) {
	cfg := &config.Config{}
	cfg.Claude.Model = "claude-opus-4-6"
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
