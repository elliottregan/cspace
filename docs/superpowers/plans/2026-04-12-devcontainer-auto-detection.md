# Devcontainer Auto-Detection & Service Rename Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename the main compose service from `devcontainer` to `cspace`, and auto-detect project services and post-setup hooks from `.devcontainer/` so projects don't need explicit config.

**Architecture:** The service rename is a string replacement across 4 source files plus the compose template. Auto-detection adds fallback paths in `ComposeFiles()` and `runPostSetup()` that check `.devcontainer/` when no explicit config is set. A constant replaces the hardcoded `"devcontainer"` string throughout the instance package.

**Tech Stack:** Go, Docker Compose YAML, shell scripts

---

### Task 1: Rename compose service from `devcontainer` to `cspace`

**Files:**
- Modify: `lib/templates/docker-compose.core.yml:7`
- Modify: `internal/instance/instance.go:114,203,227,249,268,293,315`
- Modify: `internal/supervisor/launch.go:48,94,128,241`

- [ ] **Step 1: Add a service name constant in instance.go**

Add a package-level constant at the top of `internal/instance/instance.go`, after the existing `GlobalInstanceLabel` constant:

```go
// ServiceName is the Docker Compose service name for the main cspace container.
const ServiceName = "cspace"
```

- [ ] **Step 2: Replace all hardcoded `"devcontainer"` strings in instance.go**

Replace every occurrence of the string `"devcontainer"` in `internal/instance/instance.go` with `ServiceName`:

- Line 114: `if strings.TrimSpace(line) == "devcontainer"` → `if strings.TrimSpace(line) == ServiceName`
- Line 203: `"devcontainer",` → `ServiceName,`
- Line 227: `"devcontainer",` → `ServiceName,`
- Line 249: `ports.GetHostPort(composeName, "devcontainer", port)` → `ports.GetHostPort(composeName, ServiceName, port)`
- Line 268: `if svc == "" || svc == "devcontainer" || len(parts) < 2` → `if svc == "" || svc == ServiceName || len(parts) < 2`
- Line 293: `"devcontainer",` → `ServiceName,`
- Line 315: `"cp", hostPath, "devcontainer:"+containerPath` → `"cp", hostPath, ServiceName+":"+containerPath`

- [ ] **Step 3: Replace all hardcoded `"devcontainer"` strings in launch.go**

In `internal/supervisor/launch.go`, replace every `"devcontainer"` with `instance.ServiceName`:

- Line 48: `"devcontainer",` → `instance.ServiceName,`
- Line 94: `"devcontainer",` → `instance.ServiceName,`
- Line 128: `"devcontainer",` → `instance.ServiceName,`
- Line 241: `"devcontainer",` → `instance.ServiceName,`

- [ ] **Step 4: Rename the service in docker-compose.core.yml**

In `lib/templates/docker-compose.core.yml`, change line 7:

```yaml
  cspace:
```

Keep `devcontainer` as a DNS alias for backwards compatibility by adding to the `networks` section for the default network. Update the comment on line 1 accordingly.

Actually — Docker Compose doesn't support DNS aliases directly in the networks section the same way. The service name IS the DNS name. To preserve backwards compat, add a `hostname` override won't help since it's already set to `${CSPACE_INSTANCE_NAME}`. Containers inside the compose network already address each other by service name, but the instance name is the hostname. The only consumers of the `devcontainer` DNS name would be the sidecars or project services referencing it — but they use `depends_on` and service links, not DNS names. So the rename is safe without a backwards-compat alias.

Change line 7 from `devcontainer:` to `cspace:`, and update line 1 comment:

```yaml
# Core cspace service — used by all cspace projects.
```

- [ ] **Step 5: Run tests and vet**

Run: `make test && make vet`
Expected: All tests pass, no vet warnings.

- [ ] **Step 6: Commit**

```bash
git add internal/instance/instance.go internal/supervisor/launch.go lib/templates/docker-compose.core.yml
git commit -m "Rename devcontainer service to cspace in compose template and Go code"
```

---

### Task 2: Auto-detect project compose file from `.devcontainer/`

**Files:**
- Modify: `internal/compose/compose.go:20-36` (ComposeFiles function)
- Create: `internal/compose/compose_autodetect_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/compose/compose_autodetect_test.go`:

```go
package compose

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elliottregan/cspace/internal/config"
)

func TestComposeFilesAutoDetect(t *testing.T) {
	// Create a temp project with .devcontainer/docker-compose.yml
	projectRoot := t.TempDir()
	devcontainerDir := filepath.Join(projectRoot, ".devcontainer")
	if err := os.MkdirAll(devcontainerDir, 0755); err != nil {
		t.Fatal(err)
	}
	svcFile := filepath.Join(devcontainerDir, "docker-compose.yml")
	if err := os.WriteFile(svcFile, []byte("services:\n  db:\n    image: postgres\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a fake assets dir with a core template
	assetsDir := t.TempDir()
	templatesDir := filepath.Join(assetsDir, "templates")
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		t.Fatal(err)
	}
	coreFile := filepath.Join(templatesDir, "docker-compose.core.yml")
	if err := os.WriteFile(coreFile, []byte("services:\n  cspace:\n    image: test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Services:    "", // No explicit services config
		ProjectRoot: projectRoot,
		AssetsDir:   assetsDir,
	}

	files, err := ComposeFiles(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 2 {
		t.Fatalf("ComposeFiles returned %d files, want 2", len(files))
	}
	if files[0] != coreFile {
		t.Errorf("files[0] = %q, want core template %q", files[0], coreFile)
	}
	if files[1] != svcFile {
		t.Errorf("files[1] = %q, want auto-detected %q", files[1], svcFile)
	}
}

func TestComposeFilesExplicitOverridesAutoDetect(t *testing.T) {
	// Create a temp project with both explicit and .devcontainer compose files
	projectRoot := t.TempDir()

	// .devcontainer/docker-compose.yml (should be ignored)
	devcontainerDir := filepath.Join(projectRoot, ".devcontainer")
	if err := os.MkdirAll(devcontainerDir, 0755); err != nil {
		t.Fatal(err)
	}
	autoFile := filepath.Join(devcontainerDir, "docker-compose.yml")
	if err := os.WriteFile(autoFile, []byte("services:\n  auto:\n    image: auto\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Explicit services file
	explicitFile := filepath.Join(projectRoot, "my-services.yml")
	if err := os.WriteFile(explicitFile, []byte("services:\n  explicit:\n    image: explicit\n"), 0644); err != nil {
		t.Fatal(err)
	}

	assetsDir := t.TempDir()
	templatesDir := filepath.Join(assetsDir, "templates")
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		t.Fatal(err)
	}
	coreFile := filepath.Join(templatesDir, "docker-compose.core.yml")
	if err := os.WriteFile(coreFile, []byte("services:\n  cspace:\n    image: test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Services:    "my-services.yml", // Explicit config takes priority
		ProjectRoot: projectRoot,
		AssetsDir:   assetsDir,
	}

	files, err := ComposeFiles(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 2 {
		t.Fatalf("ComposeFiles returned %d files, want 2", len(files))
	}
	if files[1] != explicitFile {
		t.Errorf("files[1] = %q, want explicit %q", files[1], explicitFile)
	}
}

func TestComposeFilesNoAutoDetect(t *testing.T) {
	// No .devcontainer dir, no explicit services — should return only core
	projectRoot := t.TempDir()

	assetsDir := t.TempDir()
	templatesDir := filepath.Join(assetsDir, "templates")
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		t.Fatal(err)
	}
	coreFile := filepath.Join(templatesDir, "docker-compose.core.yml")
	if err := os.WriteFile(coreFile, []byte("services:\n  cspace:\n    image: test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Services:    "",
		ProjectRoot: projectRoot,
		AssetsDir:   assetsDir,
	}

	files, err := ComposeFiles(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 1 {
		t.Fatalf("ComposeFiles returned %d files, want 1", len(files))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/compose/ -run TestComposeFilesAutoDetect -v`
Expected: FAIL — auto-detection not implemented yet, returns only 1 file.

- [ ] **Step 3: Implement auto-detection in ComposeFiles()**

Update `ComposeFiles()` in `internal/compose/compose.go` to add the `.devcontainer/docker-compose.yml` fallback:

```go
func ComposeFiles(cfg *config.Config) ([]string, error) {
	core, err := cfg.ResolveTemplate("docker-compose.core.yml")
	if err != nil {
		return nil, fmt.Errorf("resolving core compose file: %w", err)
	}

	files := []string{core}

	// Add project-specific services: explicit config takes priority,
	// then auto-detect from .devcontainer/docker-compose.yml.
	if cfg.Services != "" {
		svcPath := filepath.Join(cfg.ProjectRoot, cfg.Services)
		if _, err := os.Stat(svcPath); err == nil {
			files = append(files, svcPath)
		}
	} else {
		autoPath := filepath.Join(cfg.ProjectRoot, ".devcontainer", "docker-compose.yml")
		if _, err := os.Stat(autoPath); err == nil {
			files = append(files, autoPath)
		}
	}

	return files, nil
}
```

- [ ] **Step 4: Run all compose tests**

Run: `go test ./internal/compose/ -v`
Expected: All tests pass (existing + new auto-detect tests).

- [ ] **Step 5: Commit**

```bash
git add internal/compose/compose.go internal/compose/compose_autodetect_test.go
git commit -m "Auto-detect project compose file from .devcontainer/docker-compose.yml"
```

---

### Task 3: Auto-detect post-setup hook from `.devcontainer/`

**Files:**
- Modify: `internal/provision/provision.go:360-388` (runPostSetup function)

- [ ] **Step 1: Update runPostSetup() to check `.devcontainer/post-setup.sh`**

In `internal/provision/provision.go`, update `runPostSetup()`:

```go
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
	instance.DcExecRoot(composeName, "chmod", "+x", "/tmp/post-setup.sh")
	if _, err := instance.DcExec(composeName, "bash", "/tmp/post-setup.sh"); err != nil {
		return fmt.Errorf("running post-setup script: %w", err)
	}
	instance.DcExec(composeName, "touch", marker)
	fmt.Println("Post-setup complete.")
	return nil
}
```

- [ ] **Step 2: Run tests and vet**

Run: `make test && make vet`
Expected: All tests pass, no vet warnings.

- [ ] **Step 3: Commit**

```bash
git add internal/provision/provision.go
git commit -m "Auto-detect post-setup hook from .devcontainer/post-setup.sh"
```

---

### Task 4: Build, sync embedded assets, and verify

**Files:**
- No new files — this is a verification step.

- [ ] **Step 1: Sync embedded assets and build**

Run: `make build`
Expected: Build succeeds. The `make build` target runs `sync-embedded` automatically, which copies the updated `docker-compose.core.yml` into `internal/assets/embedded/`.

- [ ] **Step 2: Run full test suite**

Run: `make test && make vet`
Expected: All tests pass.

- [ ] **Step 3: Verify embedded compose template has the rename**

Run: `grep 'cspace:' internal/assets/embedded/templates/docker-compose.core.yml`
Expected: Shows `  cspace:` (the renamed service).

- [ ] **Step 4: Commit any remaining changes**

If `sync-embedded` produced changes:

```bash
git add internal/assets/embedded/
git commit -m "Sync embedded assets after service rename"
```

Note: `internal/assets/embedded/` is in `.gitignore`, so this step may be a no-op.

---

### Task 5: Integration test with resume-redux

**Files (resume-redux repo, not committed to cspace):**
- Create: `resume-redux/.devcontainer/docker-compose.yml` (Convex services only)
- Create: `resume-redux/.devcontainer/post-setup.sh` (convex-init logic)
- Rename: `resume-redux/.devcontainer/` → `resume-redux/.devcontainer-legacy/`

- [ ] **Step 1: Rename legacy devcontainer in resume-redux**

```bash
cd ~/Projects/resume-redux
mv .devcontainer .devcontainer-legacy
mkdir .devcontainer
```

- [ ] **Step 2: Create project compose file with Convex services**

Create `~/Projects/resume-redux/.devcontainer/docker-compose.yml`:

```yaml
# Project services for resume-redux.
# Layered on top of cspace's core compose template via auto-detection.
# Only define additional services here — do NOT redefine the cspace service.
#
# Available env vars (from cspace's ComposeEnv):
#   CSPACE_CONTAINER_NAME — compose project name, e.g. "re-mercury"
#   COMPOSE_PROJECT_NAME  — same value (Docker Compose built-in)
#   CSPACE_PREFIX         — project prefix, e.g. "re"
#   CSPACE_PROJECT_NAME   — project name, e.g. "resume-redux"
#   CSPACE_INSTANCE_NAME  — instance name, e.g. "mercury"

services:
  convex-backend:
    image: ghcr.io/get-convex/convex-backend:latest
    container_name: ${CSPACE_CONTAINER_NAME}.convex
    stop_grace_period: 10s
    stop_signal: SIGINT
    ports:
      - "0:3210"
      - "0:3211"
    volumes:
      - convex-data:/convex/data
    environment:
      - CONVEX_CLOUD_ORIGIN=http://${CSPACE_CONTAINER_NAME}.convex:3210
      - CONVEX_SITE_ORIGIN=http://${CSPACE_CONTAINER_NAME}.convex:3211
    healthcheck:
      test: curl -f http://localhost:3210/version
      interval: 5s
      start_period: 10s

  convex-dashboard:
    image: ghcr.io/get-convex/convex-dashboard:latest
    container_name: ${CSPACE_CONTAINER_NAME}.convex-dash
    stop_grace_period: 10s
    stop_signal: SIGINT
    ports:
      - "0:6791"
    environment:
      - NEXT_PUBLIC_DEPLOYMENT_URL=http://127.0.0.1:3210
    depends_on:
      convex-backend:
        condition: service_healthy

volumes:
  convex-data:
```

- [ ] **Step 3: Create post-setup script with convex-init logic**

Create `~/Projects/resume-redux/.devcontainer/post-setup.sh` — extract from `.devcontainer-legacy/setup-instance.sh` lines 144-260. The cspace container has `docker-cli` and `docker-cli-compose` installed plus the Docker socket mounted (DooD), so the script can `docker exec` into sibling containers like `convex-backend`.

The script should:

1. Wait for Convex backend health via curl
2. Generate admin key via `docker exec` into the convex-backend container
3. Write `CONVEX_SELF_HOSTED_URL` and `CONVEX_SELF_HOSTED_ADMIN_KEY` to `.env.local`
4. Backup admin key to `~/.convex-env`
5. Comment out cloud `CONVEX_DEPLOYMENT`/`CONVEX_DEPLOY_KEY` in `.env`
6. Push schema with `pnpm exec convex dev --once --typecheck disable`
7. Generate RSA keypair for `@convex-dev/auth`
8. Set Convex env vars (JWT keys, Gemini key, debug pro, seed data)
9. Seed upgrade codes and E2E test data
10. Touch `/workspace/.convex-init-done`

The convex-backend container name is `${CSPACE_CONTAINER_NAME}.convex` (e.g., `re-mercury.convex`). `CSPACE_CONTAINER_NAME` is available inside the cspace container via the `environment:` block in `docker-compose.core.yml`. Use `docker exec` to run commands in sibling containers (e.g., `docker exec ${CSPACE_CONTAINER_NAME}.convex ./generate_admin_key.sh`) — this works because the cspace container has `docker-cli` installed and the Docker socket mounted.

Adapt the legacy script (`setup-instance.sh` lines 144-260), replacing `$DC exec -T convex-backend` with `docker exec ${CSPACE_CONTAINER_NAME}.convex`. Replace `$DC exec -T -u dev devcontainer` commands with direct execution (since the script already runs inside the cspace container as `dev`).

- [ ] **Step 4: Tear down any running instances and test**

```bash
cd ~/Projects/resume-redux
cspace down --all
cspace rebuild
cspace up mercury --no-claude
```

Verify:
- Convex backend container starts alongside cspace container
- Post-setup runs and provisions the database
- `.convex-init-done` marker is created
- E2E tests can run

- [ ] **Step 5: Clean up**

Restore resume-redux to original state if not committing changes:
```bash
rm -rf ~/Projects/resume-redux/.devcontainer
mv ~/Projects/resume-redux/.devcontainer-legacy ~/Projects/resume-redux/.devcontainer
```
