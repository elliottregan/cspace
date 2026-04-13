// Package compose handles Docker Compose file resolution, environment variable
// export, and command construction for cspace instances.
package compose

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/elliottregan/cspace/internal/config"
	"github.com/elliottregan/cspace/internal/ports"
)

// ComposeFiles resolves the compose file list for an instance.
// Returns the file paths in order: core.yml, then optional project services.
func ComposeFiles(cfg *config.Config) ([]string, error) {
	core, err := cfg.ResolveTemplate("docker-compose.core.yml")
	if err != nil {
		return nil, fmt.Errorf("resolving core compose file: %w", err)
	}

	files := []string{core}

	// Add project-specific services if configured
	if cfg.Services != "" {
		svcPath := filepath.Join(cfg.ProjectRoot, cfg.Services)
		if _, err := os.Stat(svcPath); err == nil {
			files = append(files, svcPath)
		}
	}

	return files, nil
}

// ComposeEnv computes the environment variables needed by compose templates.
// Returns a slice of KEY=VALUE strings.
func ComposeEnv(name string, cfg *config.Config) []string {
	pm := ports.AssignPorts(name)

	// Derive CSPACE_HOME from AssetsDir (assetsDir is <home>/lib, so parent is home)
	cspaceHome := filepath.Dir(cfg.AssetsDir)

	env := []string{
		"COMPOSE_PROJECT_NAME=" + cfg.ComposeName(name),
		"CSPACE_CONTAINER_NAME=" + cfg.ComposeName(name),
		"CSPACE_INSTANCE_NAME=" + name,
		"CSPACE_PROJECT_NAME=" + cfg.Project.Name,
		"CSPACE_PREFIX=" + cfg.Project.Prefix,
		"CSPACE_IMAGE=" + cfg.ImageName(),
		"CSPACE_MEMORY_VOLUME=" + cfg.MemoryVolume(),
		"CSPACE_LOGS_VOLUME=" + cfg.LogsVolume(),
		"CSPACE_LABEL=" + cfg.InstanceLabel(),
		"CSPACE_PROJECT_NETWORK=" + cfg.ProjectNetwork(),
		"CSPACE_HOME=" + cspaceHome,
		"HOST_PORT_DEV=" + strconv.Itoa(pm.Dev),
		"HOST_PORT_PREVIEW=" + strconv.Itoa(pm.Preview),
		"PROJECT_ROOT=" + cfg.ProjectRoot,
	}

	// Export container environment from config as CSPACE_ENV_* variables
	for k, v := range cfg.Container.Environment {
		env = append(env, "CSPACE_ENV_"+k+"="+v)
	}

	// Firewall domains (space-separated for the container environment)
	if len(cfg.Firewall.Domains) > 0 {
		env = append(env, "CSPACE_FIREWALL_DOMAINS="+strings.Join(cfg.Firewall.Domains, " "))
	}

	// Claude model and effort
	env = append(env, "CSPACE_CLAUDE_MODEL="+cfg.Claude.Model)
	env = append(env, "CSPACE_CLAUDE_EFFORT="+cfg.Claude.Effort)

	// MCP servers as compact JSON (nil map marshals to "null", normalize to "{}")
	mcpJSON, _ := json.Marshal(cfg.MCPServers)
	if len(mcpJSON) == 0 || string(mcpJSON) == "null" {
		mcpJSON = []byte("{}")
	}
	env = append(env, "CSPACE_MCP_SERVERS="+string(mcpJSON))

	// Resolved Dockerfile path (used by docker-compose.core.yml for build)
	if dockerfilePath, err := cfg.ResolveTemplate("Dockerfile"); err == nil {
		env = append(env, "CSPACE_DOCKERFILE="+dockerfilePath)
	}

	return env
}

// Cmd constructs an *exec.Cmd for running docker compose with proper file
// resolution and environment for the given instance.
// The returned command is not started -- the caller can attach stdio or run it.
func Cmd(name string, cfg *config.Config, args ...string) (*exec.Cmd, error) {
	files, err := ComposeFiles(cfg)
	if err != nil {
		return nil, err
	}

	composeName := cfg.ComposeName(name)

	// Build the full argument list: compose -f file1 -f file2 -p project <args...>
	cmdArgs := make([]string, 0, 1+2*len(files)+2+len(args))
	cmdArgs = append(cmdArgs, "compose")
	for _, f := range files {
		cmdArgs = append(cmdArgs, "-f", f)
	}
	cmdArgs = append(cmdArgs, "-p", composeName)
	cmdArgs = append(cmdArgs, args...)

	cmd := exec.Command("docker", cmdArgs...)

	// Merge compose env vars with the current environment
	cmd.Env = append(os.Environ(), ComposeEnv(name, cfg)...)

	return cmd, nil
}

// Run constructs and runs a docker compose command for the given instance.
// Stdout and stderr are inherited from the parent process.
func Run(name string, cfg *config.Config, args ...string) error {
	cmd, err := Cmd(name, cfg, args...)
	if err != nil {
		return err
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// RunDirect runs docker compose with an explicit project name, without
// compose file resolution or environment export. Used for cross-project
// operations like `down --everywhere` where we only have the compose
// project name from Docker labels.
func RunDirect(composeProject string, args ...string) error {
	cmdArgs := make([]string, 0, 3+len(args))
	cmdArgs = append(cmdArgs, "compose", "-p", composeProject)
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command("docker", cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
