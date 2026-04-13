# Auto-detection of project services and post-setup hooks

**Date:** 2026-04-12
**Status:** Approved

## Problem

cspace's Go provisioner ignores project-specific infrastructure (databases, init scripts) unless explicitly configured via `services` and `post_setup` in `.cspace.json`. Projects migrating from legacy setups (like resume-redux's `.devcontainer/setup-instance.sh`) get containers without their databases provisioned.

## Design

### Service rename: `devcontainer` → `cspace`

Rename the main service in `docker-compose.core.yml` from `devcontainer` to `cspace`. Changes propagate to:

- **`lib/templates/docker-compose.core.yml`** — service name. Keep `devcontainer` as a DNS alias for backwards compatibility with anything inside containers that references it by hostname.
- **`internal/instance/`** — all `docker compose exec` calls that target the service by name.
- Any other references to the `devcontainer` service name throughout the codebase.

### Auto-detection of project compose file

`ComposeFiles()` in `internal/compose/compose.go` resolves the compose file list. After the core template, it checks for a project services file in priority order:

1. **Explicit config** — `cfg.Services` path from `.cspace.json` (unchanged behavior)
2. **Convention** — `$PROJECT_ROOT/.devcontainer/docker-compose.yml` (new auto-detection)

If found, the file is passed as an additional `-f` flag to Docker Compose, which handles the merge natively. The project file must only define additional services — it must not redefine the `cspace` service. Existing compose env vars (`COMPOSE_PROJECT_NAME`, `CSPACE_PREFIX`, `HOST_PORT_*`, etc.) are available for use in the project file.

### Auto-detection of post-setup hook

`runPostSetup()` in `internal/provision/provision.go` checks for a post-setup script in priority order:

1. **Explicit config** — `cfg.PostSetup` path from `.cspace.json` (unchanged behavior)
2. **Convention** — `$PROJECT_ROOT/.devcontainer/post-setup.sh` (new auto-detection)

The script is copied into the container and executed as the `dev` user, same as today. The idempotency marker remains `/workspace/.cspace-post-setup-done`. The script runs after workspace init, env file copy, and gh auth setup — so `.env`, `.env.local`, `GH_TOKEN`, and the full workspace are available.

## Files changed (cspace)

- `lib/templates/docker-compose.core.yml` — rename service `devcontainer` → `cspace`
- `internal/compose/compose.go` — add `.devcontainer/docker-compose.yml` fallback in `ComposeFiles()`
- `internal/provision/provision.go` — add `.devcontainer/post-setup.sh` fallback in `runPostSetup()`
- `internal/instance/` — update service name references from `devcontainer` to `cspace`
- Any other files referencing the `devcontainer` service name

## Testing (resume-redux)

To validate end-to-end:

1. Rename `.devcontainer/` → `.devcontainer-legacy/` in resume-redux
2. Create `.devcontainer/docker-compose.yml` with only Convex services (backend + dashboard), using `CSPACE_PREFIX`/`COMPOSE_PROJECT_NAME` env vars instead of hardcoded prefixes
3. Create `.devcontainer/post-setup.sh` with the convex-init logic extracted from the legacy `setup-instance.sh`
4. `cspace rebuild && cspace up` — verify Convex backend starts and database is provisioned
