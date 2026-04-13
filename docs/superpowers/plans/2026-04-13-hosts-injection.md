# Hosts Injection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `cspace.local` hostnames work from inside containers by injecting Traefik's Docker IP into `/etc/hosts`, so the same URL works from both the host browser and container-side clients.

**Architecture:** After compose up + readiness check, a new `InjectHosts()` function resolves Traefik's IP on the project network, builds a list of `cspace.local` hostnames (core + project services from Traefik labels), and injects them into `/etc/hosts` of all containers in the stack. The convex-nuxt template gets Traefik labels on `convex-backend` and sets `VITE_CONVEX_URL` to the routable cspace.local hostname.

**Tech Stack:** Go, Docker CLI, bash, Docker Compose YAML

---

### Task 1: Add InjectHosts helper to docker.go

**Files:**
- Modify: `internal/docker/docker.go`

- [ ] **Step 1: Add GetContainerIP helper**

Add to `internal/docker/docker.go` after `EnsureProxy()`:

```go
// GetContainerIP returns a container's IP address on the given network.
func GetContainerIP(container, network string) (string, error) {
	out, err := exec.Command(
		"docker", "inspect", container,
		"--format", fmt.Sprintf(`{{(index .NetworkSettings.Networks "%s").IPAddress}}`, network),
	).Output()
	if err != nil {
		return "", fmt.Errorf("getting IP of %s on %s: %w", container, network, err)
	}
	ip := strings.TrimSpace(string(out))
	if ip == "" || ip == "<no value>" {
		return "", fmt.Errorf("%s is not connected to network %s", container, network)
	}
	return ip, nil
}
```

- [ ] **Step 2: Add GetTraefikHostnames helper**

Add to `internal/docker/docker.go`:

```go
// GetTraefikHostnames discovers cspace.local hostnames from Traefik labels
// on containers in a compose project. Returns deduplicated hostnames.
func GetTraefikHostnames(composeName string) []string {
	out, err := exec.Command(
		"docker", "compose", "-p", composeName,
		"ps", "-q",
	).Output()
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var hostnames []string

	for _, id := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if id == "" {
			continue
		}
		labelOut, err := exec.Command(
			"docker", "inspect", id,
			"--format", `{{range $k, $v := .Config.Labels}}{{$k}}={{$v}}{{"\n"}}{{end}}`,
		).Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(labelOut), "\n") {
			// Match traefik.http.routers.*.rule=Host(`hostname`)
			if !strings.Contains(line, "traefik.http.routers.") || !strings.Contains(line, ".rule=") {
				continue
			}
			// Extract hostname from Host(`...`)
			start := strings.Index(line, "Host(`")
			if start < 0 {
				continue
			}
			start += len("Host(`")
			end := strings.Index(line[start:], "`)")
			if end < 0 {
				continue
			}
			hostname := line[start : start+end]
			if !seen[hostname] {
				seen[hostname] = true
				hostnames = append(hostnames, hostname)
			}
		}
	}
	return hostnames
}
```

- [ ] **Step 3: Add InjectHosts function**

Add to `internal/docker/docker.go`:

```go
// InjectHosts injects /etc/hosts entries into all containers in a compose
// stack, mapping cspace.local hostnames to Traefik's IP on the project
// network. This makes cspace.local URLs work from inside containers
// (where CoreDNS's 127.0.0.1 response doesn't reach Traefik).
func InjectHosts(composeName, projectNetwork string) error {
	proxyIP, err := GetContainerIP(ProxyContainerName, projectNetwork)
	if err != nil {
		return fmt.Errorf("resolving proxy IP: %w", err)
	}

	hostnames := GetTraefikHostnames(composeName)
	if len(hostnames) == 0 {
		return nil
	}

	hostsLine := proxyIP + " " + strings.Join(hostnames, " ")

	// Get all container IDs in the compose project
	out, err := exec.Command(
		"docker", "compose", "-p", composeName,
		"ps", "-q",
	).Output()
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}

	for _, id := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if id == "" {
			continue
		}
		// Remove old cspace.local entries and append fresh ones
		script := fmt.Sprintf(
			`sed -i '/cspace\.local/d' /etc/hosts && echo '%s' >> /etc/hosts`,
			hostsLine,
		)
		exec.Command("docker", "exec", id, "sh", "-c", script).Run() //nolint:errcheck
	}

	return nil
}
```

- [ ] **Step 4: Run tests and vet**

Run: `make test && make vet`
Expected: All pass.

- [ ] **Step 5: Commit**

```bash
git add internal/docker/docker.go
git commit -m "Add InjectHosts helper for cspace.local resolution inside containers"
```

---

### Task 2: Call InjectHosts during provisioning

**Files:**
- Modify: `internal/provision/provision.go`

- [ ] **Step 1: Add InjectHosts call after readiness check**

In `internal/provision/provision.go`, after step 8 (Wait for readiness, around line 131) and before step 9 (Fix volume ownership), add:

```go
		// 8b. Inject /etc/hosts entries so cspace.local hostnames resolve
		// to Traefik's Docker IP inside all containers.
		if err := docker.InjectHosts(composeName, cfg.ProjectNetwork()); err != nil {
			fmt.Fprintf(os.Stderr, "warning: hosts injection: %v\n", err)
		}
```

- [ ] **Step 2: Update the doc comment step list**

In the `Run()` doc comment (around line 38), update the steps list. After step 8 add:

```
//  8b. Inject /etc/hosts for cspace.local resolution inside containers
```

- [ ] **Step 3: Run tests and vet**

Run: `make test && make vet`
Expected: All pass.

- [ ] **Step 4: Commit**

```bash
git add internal/provision/provision.go
git commit -m "Inject cspace.local hosts entries into containers during provisioning"
```

---

### Task 3: Add Traefik labels to convex-nuxt template

**Files:**
- Modify: `lib/templates/devcontainer/convex-nuxt/docker-compose.yml`

- [ ] **Step 1: Add Traefik labels to convex-backend**

In `lib/templates/devcontainer/convex-nuxt/docker-compose.yml`, add Traefik labels to the `convex-backend` service. Replace the current `convex-backend` definition (lines 19-35):

```yaml
  # Self-hosted Convex backend — one per instance, data in a named volume.
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
    labels:
      - "traefik.enable=true"
      - "traefik.docker.network=${CSPACE_PROJECT_NETWORK}"
      - "traefik.http.routers.${CSPACE_CONTAINER_NAME}-convex.rule=Host(`convex.${CSPACE_INSTANCE_NAME}.${CSPACE_PROJECT_NAME}.cspace.local`)"
      - "traefik.http.routers.${CSPACE_CONTAINER_NAME}-convex.entrypoints=web"
      - "traefik.http.routers.${CSPACE_CONTAINER_NAME}-convex.service=${CSPACE_CONTAINER_NAME}-convex"
      - "traefik.http.services.${CSPACE_CONTAINER_NAME}-convex.loadbalancer.server.port=3210"
    healthcheck:
      test: curl -f http://localhost:3210/version
      interval: 5s
      start_period: 10s
```

- [ ] **Step 2: Commit**

```bash
git add lib/templates/devcontainer/convex-nuxt/docker-compose.yml
git commit -m "Add Traefik labels to convex-backend in convex-nuxt template"
```

---

### Task 4: Update convex-nuxt post-setup to set routable VITE_CONVEX_URL

**Files:**
- Modify: `lib/templates/devcontainer/convex-nuxt/post-setup.sh`

- [ ] **Step 1: Add VITE_CONVEX_URL to the post-setup script**

In `lib/templates/devcontainer/convex-nuxt/post-setup.sh`, after the block that writes `CONVEX_SELF_HOSTED_URL` and `CONVEX_SELF_HOSTED_ADMIN_KEY` to `.env.local` (around line 35), add a line that also writes `VITE_CONVEX_URL`:

After:
```bash
printf '\n# Local Convex backend (auto-generated by post-setup.sh)\nCONVEX_SELF_HOSTED_URL=http://%s:3210\nCONVEX_SELF_HOSTED_ADMIN_KEY=%s\n' "$CONVEX_CONTAINER" "$ADMIN_KEY" \
    >> /workspace/.env.local
```

Change to:
```bash
CONVEX_PUBLIC_URL="http://convex.${CSPACE_INSTANCE_NAME:-$(hostname)}.${CSPACE_PROJECT_NAME:-cspace}.cspace.local"

printf '\n# Local Convex backend (auto-generated by post-setup.sh)\nCONVEX_SELF_HOSTED_URL=http://%s:3210\nCONVEX_SELF_HOSTED_ADMIN_KEY=%s\nVITE_CONVEX_URL=%s\n' "$CONVEX_CONTAINER" "$ADMIN_KEY" "$CONVEX_PUBLIC_URL" \
    >> /workspace/.env.local
```

Also clean up `VITE_CONVEX_URL` in the partial state cleanup block at the top. Change line 10:

```bash
sed -i '/^# Local Convex backend/d; /^CONVEX_SELF_HOSTED_URL=/d; /^CONVEX_SELF_HOSTED_ADMIN_KEY=/d; /^VITE_CONVEX_URL=/d' /workspace/.env.local 2>/dev/null || true
```

- [ ] **Step 2: Commit**

```bash
git add lib/templates/devcontainer/convex-nuxt/post-setup.sh
git commit -m "Set VITE_CONVEX_URL to routable cspace.local hostname in post-setup"
```

---

### Task 5: Build, sync embedded assets, and integration test

**Files:**
- No new files — verification step.

- [ ] **Step 1: Build**

Run: `make build`
Expected: Build succeeds.

- [ ] **Step 2: Run tests**

Run: `make test && make vet`
Expected: All pass.

- [ ] **Step 3: Integration test with resume-redux**

```bash
# Tear down venus if still running
cd ~/Projects/resume-redux && cspace down venus

# Start fresh instance
cspace up venus --no-claude
```

After provisioning:

```bash
# Verify hosts entries were injected
docker exec re-venus grep cspace.local /etc/hosts
docker exec re-venus.playwright grep cspace.local /etc/hosts

# Verify Convex is routable through Traefik from host
curl -s http://convex.venus.resume-redux.cspace.local/version

# Verify from inside cspace container
docker exec re-venus curl -s http://convex.venus.resume-redux.cspace.local/version

# Verify from Playwright sidecar
docker exec re-venus.playwright curl -s http://convex.venus.resume-redux.cspace.local/version

# Check VITE_CONVEX_URL was set correctly
docker exec re-venus grep VITE_CONVEX_URL /workspace/.env.local
```

Expected:
- All three contexts return Convex version JSON
- `VITE_CONVEX_URL=http://convex.venus.resume-redux.cspace.local`

- [ ] **Step 4: Commit any remaining changes**

```bash
git add -A && git commit -m "Sync embedded assets for hosts injection"
```
