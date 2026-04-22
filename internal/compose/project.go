package compose

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/elliottregan/cspace/internal/config"
)

// ProjectStackEnv computes the environment variables needed by the project
// stack compose template (docker-compose.project.yml).
func ProjectStackEnv(cfg *config.Config) []string {
	cspaceHome := filepath.Dir(cfg.AssetsDir)

	return []string{
		"COMPOSE_PROJECT_NAME=" + cfg.ProjectStackName(),
		"CSPACE_PROJECT_STACK_NAME=" + cfg.ProjectStackName(),
		"CSPACE_PROJECT_NAME=" + cfg.Project.Name,
		"CSPACE_PROJECT_NETWORK=" + cfg.ProjectNetwork(),
		"CSPACE_HOME=" + cspaceHome,
	}
}

// ProjectStackUp starts the project-scoped search sidecar stack.
// Idempotent — reusing an already-running stack is a no-op.
func ProjectStackUp(cfg *config.Config) error {
	composeFile, err := cfg.ResolveTemplate("docker-compose.project.yml")
	if err != nil {
		return fmt.Errorf("resolving project stack compose file: %w", err)
	}

	stackName := cfg.ProjectStackName()
	cmd := exec.Command("docker", "compose",
		"-f", composeFile,
		"-p", stackName,
		"up", "-d",
	)
	cmd.Env = append(os.Environ(), ProjectStackEnv(cfg)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("starting project stack %s: %w", stackName, err)
	}
	return nil
}

// ProjectStackDown tears down the project-scoped search sidecar stack,
// removing its containers and volumes.
func ProjectStackDown(cfg *config.Config) error {
	composeFile, err := cfg.ResolveTemplate("docker-compose.project.yml")
	if err != nil {
		return fmt.Errorf("resolving project stack compose file: %w", err)
	}

	stackName := cfg.ProjectStackName()
	cmd := exec.Command("docker", "compose",
		"-f", composeFile,
		"-p", stackName,
		"down", "--volumes",
	)
	cmd.Env = append(os.Environ(), ProjectStackEnv(cfg)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("stopping project stack %s: %w", stackName, err)
	}
	return nil
}

// ProjectStackRunning checks whether any container belonging to the
// project stack is currently running.
func ProjectStackRunning(cfg *config.Config) bool {
	stackName := cfg.ProjectStackName()
	out, err := exec.Command(
		"docker", "compose",
		"-p", stackName,
		"ps", "--status", "running", "-q",
	).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// ProjectStackDownDirect tears down a project stack by compose project name
// alone, without needing config or compose file resolution. Used by
// `cspace down --everywhere` where we only have the stack name from Docker
// labels.
func ProjectStackDownDirect(stackName string) error {
	cmd := exec.Command("docker", "compose",
		"-p", stackName,
		"down", "--volumes",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
