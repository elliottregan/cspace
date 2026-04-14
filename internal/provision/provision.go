// Package provision implements cspace devcontainer provisioning.
// It is the Go port of lib/scripts/setup-instance.sh.
package provision

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/compose"
	"github.com/elliottregan/cspace/internal/config"
	"github.com/elliottregan/cspace/internal/docker"
	"github.com/elliottregan/cspace/internal/instance"
)

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Params holds everything needed to provision an instance.
type Params struct {
	Name   string         // Instance name (validated: alphanumeric + hyphens/underscores)
	Branch string         // Git branch to checkout (empty = host's current branch)
	Cfg    *config.Config // Merged configuration
}

// Result holds the outcome of a provisioning run.
type Result struct {
	Created bool   // true if container was newly created, false if already running
	Name    string // Instance name
}

// Run provisions a cspace devcontainer instance. Idempotent — safe to
// re-run on a partially configured instance.
//
// Steps:
//  1. Validate instance name
//  2. If already running, skip container creation
//  3. Remove orphaned containers, detect branch, create git bundle
//  4. Create shared Docker volumes
//  5. Create project network
//  6. Start global reverse proxy, connect to project network
//  7. docker compose up -d
//  8. Wait for container readiness
//     8b. Inject /etc/hosts for cspace.local resolution inside containers
//  9. Fix volume ownership
//  10. Copy bundle into container, init workspace
//  11. Configure git identity
//  12. Copy .env files
//  13. Setup GH_TOKEN + gh auth
//  14. Idempotent: marketplace, plugins, post-setup hook
func Run(p Params) (Result, error) {
	name := p.Name
	cfg := p.Cfg

	// 1. Validate instance name
	if err := validateName(name); err != nil {
		return Result{}, err
	}

	composeName := cfg.ComposeName(name)
	created := false

	// 2. Check if already running
	if instance.IsRunning(composeName) {
		fmt.Printf("Instance '%s' already running — checking configuration...\n", name)
	} else {
		created = true
		fmt.Printf("Creating new instance '%s'...\n", name)

		// 3. Remove orphaned containers from a previous instance with the
		// same name. Handles partial teardowns where docker compose down
		// didn't fully clean up. Refuses to remove running containers to
		// prevent accidentally destroying a live instance. Runs before the
		// expensive git bundle so we fail fast.
		containerName := cfg.ComposeName(name)
		for _, suffix := range []string{"", ".browser"} {
			if err := docker.RemoveOrphanContainer(containerName + suffix); err != nil {
				return Result{}, fmt.Errorf("refusing to provision '%s': %w", name, err)
			}
		}

		// 4. Detect branch and remote URL
		branch := p.Branch
		if branch == "" {
			branch = gitCurrentBranch(cfg.ProjectRoot)
		}
		remoteURL := gitRemoteURL(cfg.ProjectRoot)

		// Create git bundle
		bundlePath := filepath.Join(os.TempDir(), fmt.Sprintf("cspace-%s.bundle", name))
		fmt.Printf("Bundling repo (branch: %s)...\n", branch)
		if err := gitBundleCreate(cfg.ProjectRoot, bundlePath); err != nil {
			return Result{}, fmt.Errorf("creating git bundle: %w", err)
		}
		defer func() { _ = os.Remove(bundlePath) }()

		// 4. Create shared volumes
		if err := ensureVolumes(cfg); err != nil {
			return Result{}, fmt.Errorf("creating volumes: %w", err)
		}

		// 5. Ensure the shared project network exists before starting the
		// Compose stack. The cspace service declares this as an external
		// network in docker-compose.core.yml, so it must exist before compose up.
		if err := docker.NetworkCreate(cfg.ProjectNetwork(), cfg.InstanceLabel()); err != nil {
			return Result{}, fmt.Errorf("creating project network: %w", err)
		}

		// 6. Start the global reverse proxy and connect it to the project network.
		if err := docker.EnsureProxy(cfg.AssetsDir); err != nil {
			fmt.Fprintf(os.Stderr, "warning: proxy: %v\n", err)
		}

		// Connect the proxy to this project's network so Traefik can
		// route traffic to instance containers.
		if err := docker.NetworkConnect(cfg.ProjectNetwork(), docker.ProxyContainerName); err != nil {
			fmt.Fprintf(os.Stderr, "warning: connecting proxy to project network: %v\n", err)
		}

		// Ensure the teleport transfer directory exists and is exported
		// to the compose environment for the bind mount.
		tpDir := teleportHostDir()
		if err := ensureTeleportDir(tpDir); err != nil {
			return Result{}, err
		}
		os.Setenv("CSPACE_TELEPORT_DIR", tpDir)

		// 7. Start instance containers
		if err := compose.Run(name, cfg, "up", "-d"); err != nil {
			return Result{}, fmt.Errorf("starting container: %w", err)
		}

		// 8. Wait for readiness
		fmt.Println("Waiting for container...")
		if err := WaitForReady(composeName, 120*time.Second); err != nil {
			return Result{}, err
		}

		// 8b. Inject /etc/hosts entries so cspace.local hostnames resolve
		// to Traefik's Docker IP inside all containers.
		if err := docker.InjectHosts(composeName, cfg.ProjectNetwork()); err != nil {
			fmt.Fprintf(os.Stderr, "warning: hosts injection: %v\n", err)
		}

		// 9. Fix volume ownership
		if _, err := instance.DcExecRoot(composeName, "chown", "-R", "dev:dev", "/workspace", "/home/dev/.claude"); err != nil {
			return Result{}, fmt.Errorf("fixing ownership: %w", err)
		}

		// 10. Copy bundle and init workspace
		if err := initWorkspace(composeName, bundlePath, branch, remoteURL); err != nil {
			return Result{}, fmt.Errorf("initializing workspace: %w", err)
		}

		// 11. Configure git identity from host
		configureGit(composeName, cfg.ProjectRoot)

		// 12. Copy .env files
		copyEnvFile(composeName, cfg.ProjectRoot, ".env")
		copyEnvFile(composeName, cfg.ProjectRoot, ".env.local")

		// 13. Setup GH_TOKEN + gh auth (warn on failure, don't block provisioning)
		if err := setupGHAuth(composeName, cfg.ProjectRoot); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		}
	}

	// --- Idempotent stages (run even if container was already running) ---

	// 14a. Ensure plugin marketplace
	if err := ensureMarketplace(composeName); err != nil {
		fmt.Fprintf(os.Stderr, "warning: marketplace setup: %v\n", err)
	}

	// 14b. Install recommended plugins
	if err := installPlugins(composeName, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "warning: plugin installation: %v\n", err)
	}

	// 14c. Run post-setup hook
	if err := runPostSetup(composeName, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "warning: post-setup: %v\n", err)
	}

	fmt.Println("Setup complete.")
	return Result{Created: created, Name: name}, nil
}

// WaitForReady polls the container until docker exec succeeds, with timeout.
func WaitForReady(composeName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := instance.DcExecRoot(composeName, "true"); err == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("container did not become ready within %v", timeout)
}

// validateName checks that the instance name matches ^[a-zA-Z0-9_-]+$.
func validateName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("instance name must be alphanumeric (hyphens and underscores allowed), got: %s", name)
	}
	return nil
}

// gitCurrentBranch returns the current branch of the project repo.
func gitCurrentBranch(projectRoot string) string {
	out, err := exec.Command("git", "-C", projectRoot, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "main"
	}
	return strings.TrimSpace(string(out))
}

// gitRemoteURL returns the origin remote URL with embedded credentials stripped.
func gitRemoteURL(projectRoot string) string {
	out, err := exec.Command("git", "-C", projectRoot, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return stripCredentials(strings.TrimSpace(string(out)))
}

// stripCredentials removes embedded auth credentials from a URL.
// Converts "https://user:pass@host/path" to "https://host/path".
func stripCredentials(url string) string {
	if idx := strings.Index(url, "://"); idx >= 0 {
		rest := url[idx+3:]
		if atIdx := strings.Index(rest, "@"); atIdx >= 0 {
			url = url[:idx+3] + rest[atIdx+1:]
		}
	}
	return url
}

// ensureTeleportDir creates the host-side teleport transfer directory if
// it does not already exist. This path is bind-mounted into every cspace
// container at /teleport so teleport can move bundles and transcripts
// between source and target without going through docker cp.
func ensureTeleportDir(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating teleport dir %s: %w", dir, err)
	}
	return nil
}

// teleportHostDir returns the host-side path used for the /teleport bind
// mount. Defaults to ~/.cspace/teleport; overridable via CSPACE_TELEPORT_DIR.
func teleportHostDir() string {
	if v := os.Getenv("CSPACE_TELEPORT_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/cspace-teleport"
	}
	return filepath.Join(home, ".cspace", "teleport")
}

// gitBundleCreate creates a git bundle of the entire repo.
func gitBundleCreate(projectRoot, bundlePath string) error {
	cmd := exec.Command("git", "-C", projectRoot, "bundle", "create", bundlePath, "--all")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// gitConfigValue reads a git config value from the project repo.
func gitConfigValue(projectRoot, key string) string {
	out, _ := exec.Command("git", "-C", projectRoot, "config", key).Output()
	return strings.TrimSpace(string(out))
}

// ensureVolumes creates the shared external volumes if they don't already exist.
func ensureVolumes(cfg *config.Config) error {
	for _, vol := range []string{cfg.MemoryVolume(), cfg.LogsVolume()} {
		if err := docker.VolumeCreate(vol); err != nil {
			// Log but don't fail — volume may already exist
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		}
	}
	return nil
}

// initWorkspace copies the git bundle into the container and runs init-workspace.sh.
func initWorkspace(composeName, bundlePath, branch, remoteURL string) error {
	fmt.Println("Copying repo bundle into container...")
	if err := instance.DcCp(composeName, bundlePath, "/tmp/repo.bundle"); err != nil {
		return fmt.Errorf("copying bundle: %w", err)
	}
	if _, err := instance.DcExecRoot(composeName, "chown", "dev:dev", "/tmp/repo.bundle"); err != nil {
		return fmt.Errorf("chown bundle: %w", err)
	}

	fmt.Println("Initializing workspace...")
	if _, err := instance.DcExec(composeName, "init-workspace.sh", "/tmp/repo.bundle", branch, remoteURL); err != nil {
		return fmt.Errorf("init-workspace.sh: %w", err)
	}
	_, _ = instance.DcExecRoot(composeName, "rm", "-f", "/tmp/repo.bundle")
	return nil
}

// configureGit sets git user.name and user.email inside the container from host config.
func configureGit(composeName, projectRoot string) {
	if name := gitConfigValue(projectRoot, "user.name"); name != "" {
		_, _ = instance.DcExec(composeName, "git", "config", "--global", "user.name", name)
	}
	if email := gitConfigValue(projectRoot, "user.email"); email != "" {
		_, _ = instance.DcExec(composeName, "git", "config", "--global", "user.email", email)
	}
}

// copyEnvFile copies a single env file from the project root into the container.
func copyEnvFile(composeName, projectRoot, filename string) {
	src := filepath.Join(projectRoot, filename)
	if _, err := os.Stat(src); err != nil {
		return
	}
	if err := instance.DcCp(composeName, src, "/workspace/"+filename); err != nil {
		fmt.Fprintf(os.Stderr, "warning: copying %s: %v\n", filename, err)
		return
	}
	_, _ = instance.DcExecRoot(composeName, "chown", "dev:dev", "/workspace/"+filename)
	fmt.Printf("Copied %s\n", filename)
}

// setupGHAuth configures gh auth inside the container if GH_TOKEN is set.
// Returns a descriptive error if GH_TOKEN is missing or gh auth fails
// (callers should warn, not fail).
func setupGHAuth(composeName, projectRoot string) error {
	// Check token presence separately from auth setup so the error
	// message accurately describes the failure.
	if _, err := instance.DcExec(composeName, "bash", "-c",
		`[ -n "${GH_TOKEN:-}" ]`); err != nil {
		//nolint:staticcheck // Multi-line user-facing error; formatting trumps ST1005.
		return fmt.Errorf(`GH_TOKEN is not set in the container.

Agents in this instance will not be able to push, pull, or open PRs.

Fix:
  1. Create a GitHub token with scopes: repo, workflow, read:org
     https://github.com/settings/tokens/new?scopes=repo,workflow,read:org
  2. Add it to your project .env (or shell env):
     echo 'GH_TOKEN=ghp_...' >> %s/.env
  3. Tear down and recreate this instance:
     cspace down <name> && cspace up <name>

For SSO-protected org repos, also authorize the token for your org.`, projectRoot)
	}

	if _, err := instance.DcExec(composeName, "bash", "-c",
		`gh auth setup-git`); err != nil {
		return fmt.Errorf("gh auth setup-git failed (GH_TOKEN is set but may be invalid): %w", err)
	}

	fmt.Println("gh CLI configured for git push/pull")
	return nil
}

// ensureMarketplace clones the claude-plugins-official marketplace if not present.
func ensureMarketplace(composeName string) error {
	mdir := "/home/dev/.claude/plugins/marketplaces/claude-plugins-official"
	if _, err := instance.DcExec(composeName, "test", "-d", mdir); err == nil {
		fmt.Println("Plugin marketplace already present.")
		return nil
	}

	fmt.Println("Cloning plugin marketplace...")
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	script := fmt.Sprintf(
		`mkdir -p $(dirname %s) && git clone --depth 1 https://github.com/anthropics/claude-plugins-official.git %s && printf '{"claude-plugins-official":{"source":{"source":"github","repo":"anthropics/claude-plugins-official"},"installLocation":"%s","lastUpdated":"%s"}}' > /home/dev/.claude/plugins/known_marketplaces.json`,
		mdir, mdir, mdir, timestamp,
	)
	_, err := instance.DcExec(composeName, "bash", "-c", script)
	return err
}

// installPlugins installs recommended plugins from config, with an idempotency marker.
func installPlugins(composeName string, cfg *config.Config) error {
	if !cfg.Plugins.Enabled {
		fmt.Println("Plugin installation disabled in config.")
		return nil
	}

	marker := "/home/dev/.claude/plugins/.plugins-installed"
	if _, err := instance.DcExec(composeName, "test", "-f", marker); err == nil {
		fmt.Println("Recommended plugins already installed.")
		return nil
	}

	if len(cfg.Plugins.Install) == 0 {
		return nil
	}

	fmt.Println("Installing recommended plugins...")
	for _, plugin := range cfg.Plugins.Install {
		fmt.Printf("  - %s\n", plugin)
		// Ignore individual plugin install errors (matching bash behavior)
		_, _ = instance.DcExec(composeName, "claude", "plugin", "install", plugin)
	}

	_, _ = instance.DcExec(composeName, "touch", marker)
	fmt.Println("Plugin installation complete.")
	return nil
}

// runPostSetup copies and executes the post-setup hook if configured.
func runPostSetup(composeName string, cfg *config.Config) error {
	// Resolve post-setup script: explicit config takes priority,
	// then auto-detect from .devcontainer/post-setup.sh.
	var src string
	if cfg.PostSetup != "" {
		src = filepath.Join(cfg.ProjectRoot, cfg.PostSetup)
	} else {
		src = filepath.Join(cfg.ProjectRoot, ".devcontainer", "post-setup.sh")
	}

	if _, err := os.Stat(src); err != nil {
		return nil
	}

	marker := "/workspace/.cspace-post-setup-done"
	if _, err := instance.DcExec(composeName, "test", "-f", marker); err == nil {
		fmt.Println("Post-setup already completed.")
		return nil
	}

	fmt.Println("Running post-setup hook...")
	if err := instance.DcCp(composeName, src, "/tmp/post-setup.sh"); err != nil {
		return fmt.Errorf("copying post-setup script: %w", err)
	}
	_, _ = instance.DcExecRoot(composeName, "chmod", "+x", "/tmp/post-setup.sh")
	if err := instance.DcExecStream(composeName, "bash", "/tmp/post-setup.sh"); err != nil {
		return fmt.Errorf("running post-setup script: %w", err)
	}
	_, _ = instance.DcExec(composeName, "touch", marker)
	fmt.Println("Post-setup complete.")
	return nil
}
