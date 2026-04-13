# Traefik Reverse Proxy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a global Traefik reverse proxy with CoreDNS so every cspace instance gets a stable hostname like `mercury.resume-redux.cspace.local` that works on any Docker runtime.

**Architecture:** A shared Traefik + CoreDNS compose stack runs globally on the host. Traefik auto-discovers instance containers via Docker labels and routes HTTP requests by hostname. CoreDNS resolves `*.cspace.local` to 127.0.0.1. On each `cspace up`, the proxy is started if needed and connected to the project network.

**Tech Stack:** Traefik v3, CoreDNS, Docker Compose, Go (CLI), bash (statusline)

---

### Task 1: Create the proxy compose stack and CoreDNS config

**Files:**
- Create: `lib/templates/proxy/docker-compose.yml`
- Create: `lib/templates/proxy/Corefile`

- [ ] **Step 1: Create the CoreDNS config**

Create `lib/templates/proxy/Corefile`:

```
cspace.local {
    template IN A {
        answer "{{ .Name }} 60 IN A 127.0.0.1"
    }
}
```

- [ ] **Step 2: Create the proxy compose file**

Create `lib/templates/proxy/docker-compose.yml`:

```yaml
# Global cspace reverse proxy — shared across all projects.
# Started automatically on first `cspace up`, left running across teardowns.
# Traefik routes by hostname via Docker labels on instance containers.
# CoreDNS resolves *.cspace.local to 127.0.0.1 for the host.

services:
  traefik:
    image: traefik:v3
    container_name: cspace-proxy
    restart: unless-stopped
    command:
      - "--providers.docker=true"
      - "--providers.docker.exposedbydefault=false"
      - "--entrypoints.web.address=:80"
      - "--api.insecure=false"
      - "--log.level=WARN"
    ports:
      - "80:80"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro

  coredns:
    image: coredns/coredns:latest
    container_name: cspace-dns
    restart: unless-stopped
    command: ["-conf", "/etc/coredns/Corefile"]
    ports:
      - "127.0.0.1:53:53/udp"
      - "127.0.0.1:53:53/tcp"
    volumes:
      - ./Corefile:/etc/coredns/Corefile:ro
```

- [ ] **Step 3: Verify the compose file is valid**

Run: `docker compose -f lib/templates/proxy/docker-compose.yml config`
Expected: Parsed YAML output with no errors.

- [ ] **Step 4: Commit**

```bash
git add lib/templates/proxy/
git commit -m "Add Traefik + CoreDNS proxy compose stack and config"
```

---

### Task 2: Add Docker helpers for proxy management

**Files:**
- Modify: `internal/docker/docker.go`

- [ ] **Step 1: Add NetworkConnect helper**

Add after `NetworkRemove()` (around line 55) in `internal/docker/docker.go`:

```go
// NetworkConnect connects a container to a network. Idempotent — returns
// nil if the container is already connected.
func NetworkConnect(network, container string) error {
	cmd := exec.Command("docker", "network", "connect", network, container)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// "already exists" means the container is already connected — success
		if strings.Contains(string(out), "already exists") {
			return nil
		}
		return fmt.Errorf("connecting %s to network %s: %s", container, network, strings.TrimSpace(string(out)))
	}
	return nil
}
```

- [ ] **Step 2: Add EnsureProxy helper**

Add to `internal/docker/docker.go`:

```go
// ProxyContainerName is the Docker container name for the global Traefik proxy.
const ProxyContainerName = "cspace-proxy"

// EnsureProxy starts the global Traefik + CoreDNS proxy stack if not already
// running. The compose file is resolved from the embedded assets directory.
func EnsureProxy(assetsDir string) error {
	if IsContainerRunning(ProxyContainerName) {
		return nil
	}

	composePath := filepath.Join(assetsDir, "templates", "proxy", "docker-compose.yml")
	cmd := exec.Command("docker", "compose",
		"-f", composePath,
		"-p", "cspace-proxy",
		"up", "-d",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("starting cspace proxy: %w", err)
	}
	return nil
}
```

Ensure `"path/filepath"` is in the import block (it may already be — check before adding).

- [ ] **Step 3: Run tests and vet**

Run: `make test && make vet`
Expected: All pass. No existing tests exercise these new functions directly.

- [ ] **Step 4: Commit**

```bash
git add internal/docker/docker.go
git commit -m "Add Docker helpers for proxy management and network connect"
```

---

### Task 3: Add Traefik labels to compose template

**Files:**
- Modify: `lib/templates/docker-compose.core.yml:23-26`

- [ ] **Step 1: Add Traefik routing labels**

In `lib/templates/docker-compose.core.yml`, replace the existing `labels:` block (lines 23-26):

```yaml
    labels:
      - "cspace.instance=true"
      - "cspace.project=${CSPACE_PROJECT_NAME}"
```

With:

```yaml
    labels:
      - "cspace.instance=true"
      - "cspace.project=${CSPACE_PROJECT_NAME}"
      # Traefik routing: <instance>.<project>.cspace.local -> port 3000
      - "traefik.enable=true"
      - "traefik.http.routers.${CSPACE_CONTAINER_NAME}-dev.rule=Host(`${CSPACE_INSTANCE_NAME}.${CSPACE_PROJECT_NAME}.cspace.local`)"
      - "traefik.http.routers.${CSPACE_CONTAINER_NAME}-dev.entrypoints=web"
      - "traefik.http.services.${CSPACE_CONTAINER_NAME}-dev.loadbalancer.server.port=3000"
      # Traefik routing: preview.<instance>.<project>.cspace.local -> port 4173
      - "traefik.http.routers.${CSPACE_CONTAINER_NAME}-preview.rule=Host(`preview.${CSPACE_INSTANCE_NAME}.${CSPACE_PROJECT_NAME}.cspace.local`)"
      - "traefik.http.routers.${CSPACE_CONTAINER_NAME}-preview.entrypoints=web"
      - "traefik.http.services.${CSPACE_CONTAINER_NAME}-preview.loadbalancer.server.port=4173"
```

- [ ] **Step 2: Verify compose template is valid**

Run: `CSPACE_CONTAINER_NAME=test CSPACE_INSTANCE_NAME=mercury CSPACE_PROJECT_NAME=myproject CSPACE_IMAGE=test CSPACE_HOME=/tmp CSPACE_MEMORY_VOLUME=test CSPACE_LOGS_VOLUME=test CSPACE_PROJECT_NETWORK=test docker compose -f lib/templates/docker-compose.core.yml config 2>&1 | grep traefik`
Expected: Shows the Traefik labels with interpolated values.

- [ ] **Step 3: Commit**

```bash
git add lib/templates/docker-compose.core.yml
git commit -m "Add Traefik routing labels to core compose template"
```

---

### Task 4: Start proxy and connect network during provisioning

**Files:**
- Modify: `internal/provision/provision.go:51-162`

- [ ] **Step 1: Import the docker package if not already imported**

Check the imports in `internal/provision/provision.go`. The `docker` package should already be imported (used for `docker.NetworkCreate`). Verify it's there.

- [ ] **Step 2: Add proxy startup after network creation**

In `internal/provision/provision.go`, inside the `Run()` function, add proxy startup and network connect after the project network creation (after line 107) and before compose up (line 110).

After:
```go
if err := docker.NetworkCreate(cfg.ProjectNetwork(), cfg.InstanceLabel()); err != nil {
    return Result{}, fmt.Errorf("creating project network: %w", err)
}
```

Add:
```go
// 6b. Start the global reverse proxy if not already running.
if err := docker.EnsureProxy(cfg.AssetsDir); err != nil {
    fmt.Fprintf(os.Stderr, "warning: proxy: %v\n", err)
}

// 6c. Connect the proxy to this project's network so Traefik can
// route traffic to instance containers.
if err := docker.NetworkConnect(cfg.ProjectNetwork(), docker.ProxyContainerName); err != nil {
    fmt.Fprintf(os.Stderr, "warning: connecting proxy to project network: %v\n", err)
}
```

Note: these are warnings, not fatal errors — the proxy is a convenience feature, not required for instances to function.

- [ ] **Step 3: Run tests and vet**

Run: `make test && make vet`
Expected: All pass.

- [ ] **Step 4: Commit**

```bash
git add internal/provision/provision.go
git commit -m "Start proxy and connect to project network during provisioning"
```

---

### Task 5: Add DNS resolver setup to `cspace init`

**Files:**
- Modify: `internal/cli/init_cmd.go`

- [ ] **Step 1: Add DNS resolver check and setup**

In `internal/cli/init_cmd.go`, add a function after `scaffoldDevcontainer()`:

```go
// ensureDNSResolver checks if the macOS DNS resolver for cspace.local is
// configured, and prompts the user to set it up if not.
func ensureDNSResolver() {
	resolverPath := "/etc/resolver/cspace.local"
	if _, err := os.Stat(resolverPath); err == nil {
		return // already configured
	}

	if !isInteractive() {
		fmt.Println("Note: local DNS not configured. Run 'cspace init' interactively to set up *.cspace.local resolution.")
		return
	}

	var setupDNS bool
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Set up local DNS for *.cspace.local?").
				Description("Requires sudo (one-time). Enables hostnames like mercury.myproject.cspace.local").
				Value(&setupDNS),
		),
	)

	if err := form.Run(); err != nil || !setupDNS {
		fmt.Println("Skipped DNS setup. Service URLs will use localhost:PORT instead.")
		return
	}

	// Create resolver directory and file
	cmd := exec.Command("sudo", "mkdir", "-p", "/etc/resolver")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: creating /etc/resolver: %v\n", err)
		return
	}

	cmd = exec.Command("sudo", "tee", resolverPath)
	cmd.Stdin = strings.NewReader("nameserver 127.0.0.1\n")
	cmd.Stdout = nil // suppress tee's stdout echo
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: writing %s: %v\n", resolverPath, err)
		return
	}

	fmt.Println("Local DNS configured. *.cspace.local will resolve to 127.0.0.1.")
}
```

Ensure `"os/exec"` is in the imports.

- [ ] **Step 2: Call ensureDNSResolver from runInit**

In `runInit()`, add a call after the `.gitignore` update and before the template scaffolding (around line 190):

```go
	// Set up local DNS resolver for *.cspace.local
	ensureDNSResolver()
```

- [ ] **Step 3: Run tests and vet**

Run: `make test && make vet`
Expected: All pass.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/init_cmd.go
git commit -m "Add DNS resolver setup prompt to cspace init"
```

---

### Task 6: Update statusline to use cspace.local hostnames

**Files:**
- Modify: `lib/scripts/statusline.sh:172-210`

- [ ] **Step 1: Replace the service URL block**

In `lib/scripts/statusline.sh`, replace the entire service URL block (from `# --- Service URLs` through the `fi` before `echo`) with:

```bash
# --- Service URLs from .cspace.json's container.ports map ---
# Uses cspace.local hostnames routed by the Traefik proxy. Falls back to
# localhost:PORT via docker port if the proxy isn't running.
label_color() {
    case "$1" in
        dev)     printf '%s' "$GRN" ;;
        preview) printf '%s' "$YLW" ;;
        *)       printf '%s' "$CYN" ;;
    esac
}
CSPACE_JSON="/workspace/.cspace.json"
INSTANCE="${CSPACE_INSTANCE_NAME:-$CONTAINER}"
PROJECT="${CSPACE_PROJECT_NAME:-}"
# Check if Traefik proxy is reachable (cspace-proxy container running)
PROXY_UP=""
if docker inspect cspace-proxy --format '{{.State.Running}}' 2>/dev/null | grep -q true; then
    PROXY_UP=1
fi
if [ -n "$SELF_CONTAINER" ] && [ -f "$CSPACE_JSON" ]; then
    FIRST=1
    while IFS=$'\t' read -r internal_port label; do
        [ -z "$internal_port" ] && continue
        # Only show URLs for ports actually in use right now
        ss -tlnp 2>/dev/null | grep -q ":${internal_port} " || continue
        if [ -n "$PROXY_UP" ] && [ -n "$PROJECT" ] && [ -n "$INSTANCE" ]; then
            # Traefik hostname: first port gets bare subdomain, others get label prefix
            if [ -n "$FIRST" ]; then
                HOST="${INSTANCE}.${PROJECT}.cspace.local"
                FIRST=""
            else
                HOST="${label}.${INSTANCE}.${PROJECT}.cspace.local"
            fi
            URL="http://${HOST}"
            DISPLAY="$HOST"
        else
            # Fallback: localhost with docker port mapping
            host_port=$(docker port "$SELF_CONTAINER" "$internal_port" 2>/dev/null \
                | head -1 | awk -F: '{print $NF}')
            [ -z "$host_port" ] && continue
            URL="http://localhost:${host_port}"
            DISPLAY="localhost:${host_port}"
        fi
        printf "$DIV"
        printf "$(label_color "$label")● %s${RST} " "$label"
        link "$URL" "$DISPLAY"
    done < <(jq -r '.container.ports // {} | to_entries[] | "\(.key)\t\(.value)"' "$CSPACE_JSON" 2>/dev/null)
fi
```

- [ ] **Step 2: Remove stale OrbStack variables**

In the same file, find and remove the `ORB_HOST` and `USE_ORB` variables (around lines 184-189 from the previous version) if they still exist. The new block above replaces all of that logic.

- [ ] **Step 3: Commit**

```bash
git add lib/scripts/statusline.sh
git commit -m "Update statusline to use cspace.local hostnames with Traefik fallback"
```

---

### Task 7: Update architecture documentation

**Files:**
- Create: `docs/src/content/docs/architecture/reverse-proxy.md`
- Modify: `docs/src/content/docs/architecture/architecture-overview.md`

- [ ] **Step 1: Create reverse proxy architecture doc**

Create `docs/src/content/docs/architecture/reverse-proxy.md`:

```markdown
---
title: Reverse Proxy
description: How cspace provides per-instance hostnames via Traefik and CoreDNS
---

## Overview

cspace runs a global Traefik reverse proxy alongside a CoreDNS sidecar to give every instance a stable, human-readable hostname. This replaces the need for OrbStack-specific DNS or remembering dynamic `localhost:PORT` mappings.

## Hostnames

Each instance gets a hostname following the pattern:

```
<instance>.<project>.cspace.local
```

For example, an instance named `mercury` in a project called `resume-redux`:

| Service | URL |
|---------|-----|
| Dev server (port 3000) | `http://mercury.resume-redux.cspace.local` |
| Preview server (port 4173) | `http://preview.mercury.resume-redux.cspace.local` |

Multiple instances can all serve on the same internal port (e.g., 3000) without conflicts — each gets its own hostname.

## Architecture

Two containers run as a global shared service on the host:

- **Traefik** (`cspace-proxy`) — listens on host port 80, auto-discovers instance containers via Docker labels, and routes HTTP requests by hostname to the correct container and port.
- **CoreDNS** (`cspace-dns`) — listens on host port 53, resolves all `*.cspace.local` queries to `127.0.0.1`.

Both are defined in a single compose stack at `lib/templates/proxy/docker-compose.yml`.

## How it works

1. **DNS resolution:** The host's `/etc/resolver/cspace.local` file directs `*.cspace.local` queries to `127.0.0.1:53` where CoreDNS listens. CoreDNS returns `127.0.0.1` for all queries.

2. **HTTP routing:** The browser sends an HTTP request to `127.0.0.1:80` with a `Host` header like `mercury.resume-redux.cspace.local`. Traefik matches this against Docker labels on running containers and forwards the request to the correct container's internal port.

3. **Auto-discovery:** Instance containers in `docker-compose.core.yml` have Traefik labels that define routing rules. When a container starts or stops, Traefik updates its routing table automatically via the Docker socket.

4. **Network connectivity:** During `cspace up`, the proxy container is connected to the project's Docker network (`cspace-<project>`) so it can reach all instance containers.

## Setup

The DNS resolver is configured once per machine via `cspace init`, which creates `/etc/resolver/cspace.local` (requires sudo). The proxy containers start automatically on the first `cspace up`.

## Fallback

If the proxy is not running (e.g., first use before `cspace up`), the Claude Code statusline falls back to `localhost:PORT` URLs using `docker port` lookups.
```

- [ ] **Step 2: Add a reference in the architecture overview**

In `docs/src/content/docs/architecture/architecture-overview.md`, add a section or bullet point referencing the reverse proxy. Read the file first to find the right location, then add a brief mention like:

```markdown
### Reverse Proxy

A global [Traefik reverse proxy](/architecture/reverse-proxy/) provides per-instance hostnames (`<instance>.<project>.cspace.local`), eliminating the need to track dynamic port mappings. CoreDNS resolves `*.cspace.local` to localhost.
```

- [ ] **Step 3: Commit**

```bash
git add docs/src/content/docs/architecture/
git commit -m "Add reverse proxy architecture documentation"
```

---

### Task 8: Build, sync embedded assets, and integration test

**Files:**
- No new files — verification step.

- [ ] **Step 1: Build**

Run: `make build`
Expected: Build succeeds. Embedded assets include the new proxy compose file and Corefile.

- [ ] **Step 2: Verify embedded proxy files**

Run: `ls internal/assets/embedded/templates/proxy/`
Expected: Shows `docker-compose.yml` and `Corefile`.

- [ ] **Step 3: Run tests**

Run: `make test && make vet`
Expected: All pass.

- [ ] **Step 4: Integration test**

Test end-to-end with resume-redux:

```bash
# Tear down any running instances
cd ~/Projects/resume-redux && cspace down --all

# Start a fresh instance
cspace up mercury --no-claude

# Verify proxy is running
docker ps --filter name=cspace-proxy --filter name=cspace-dns

# Verify DNS resolution (from host)
dscacheutil -q host -a name mercury.resume-redux.cspace.local

# Verify HTTP routing (start a server inside the container first)
cspace ssh mercury
# Inside container: cd /workspace && pnpm run preview &
# From host: curl -s -o /dev/null -w "%{http_code}" http://preview.mercury.resume-redux.cspace.local
```

Expected:
- `cspace-proxy` and `cspace-dns` containers running
- DNS resolves to `127.0.0.1`
- HTTP returns 200 from the preview server

- [ ] **Step 5: Commit any remaining changes**

```bash
git add -A && git commit -m "Sync embedded assets for proxy support"
```

Note: `internal/assets/embedded/` is in `.gitignore`, so this may be a no-op.
