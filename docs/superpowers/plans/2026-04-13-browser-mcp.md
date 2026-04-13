# Consolidated Browser Sidecar with Auto-Registered MCPs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the two browser sidecars (playwright + chromium-cdp) with a single `browser` sidecar that runs Chrome, Playwright run-server, and both MCP servers — automatically registered so they work out of the box.

**Architecture:** The `browser` sidecar uses the Playwright Docker image (Ubuntu + Chrome + Node.js). Its entrypoint starts headless Chrome on port 9222 and Playwright run-server on port 3000. Both MCP servers are registered in `init-claude-plugins.sh` using `docker exec -i` to run inside the sidecar, connecting to Chrome at `localhost:9222`. The sidecar joins both `default` and `project` networks for full connectivity.

**Tech Stack:** Docker Compose, bash, Chrome DevTools Protocol

---

### Task 1: Replace two sidecars with single browser service in compose template

**Files:**
- Modify: `lib/templates/docker-compose.core.yml`

- [ ] **Step 1: Replace the playwright and chromium-cdp services with a single browser service**

In `lib/templates/docker-compose.core.yml`, delete the `playwright` service (lines 83-89) and `chromium-cdp` service (lines 91-115). Replace with:

```yaml
  # Consolidated browser sidecar — headless Chrome (CDP on 9222) + Playwright
  # run-server (3000) in one container. Both MCP servers (chrome-devtools,
  # playwright) run here via docker exec from the agent container.
  # Unrestricted network access — no firewall — so MCP browsers can reach
  # external URLs and container-hosted sites.
  browser:
    image: mcr.microsoft.com/playwright:v1.58.0-noble
    container_name: ${CSPACE_CONTAINER_NAME}.browser
    entrypoint: ["/bin/sh", "-c"]
    command:
      - |
        # Install socat for CDP port forwarding
        apt-get update -qq && apt-get install -y -qq socat >/dev/null 2>&1

        # Start headless Chrome with CDP on port 9223 (internal)
        CHROME=$$(find /ms-playwright -name "chrome" -type f -executable | head -1)
        echo "Launching Chrome: $$CHROME"
        $$CHROME \
          --headless=new \
          --no-sandbox \
          --disable-gpu \
          --disable-dev-shm-usage \
          --remote-debugging-port=9223 \
          --remote-allow-origins=* \
          --user-data-dir=/tmp/chrome-profile &

        # Wait for Chrome CDP to be ready
        until curl -sf http://127.0.0.1:9223/json/version >/dev/null; do sleep 0.5; done
        echo "CDP ready on 9223"

        # Forward 0.0.0.0:9222 -> localhost:9223 (socat rewrites Host header)
        socat TCP-LISTEN:9222,fork,reuseaddr TCP:127.0.0.1:9223 &
        echo "CDP forwarding: 0.0.0.0:9222 -> 127.0.0.1:9223"

        # Start Playwright run-server for E2E tests
        npx -y playwright@1.58.0 run-server --port 3000 --host 0.0.0.0
    networks:
      - default
      - project
    init: true
```

- [ ] **Step 2: Update the cspace service references**

In the same file, update the `cspace` service:

Change line 3-4 comment:
```yaml
# Core cspace service — used by all cspace projects.
# Project-specific services are added via compose file layering (-f).
# Browser sidecar provides Chrome CDP + Playwright run-server per instance.
```

Change `PW_TEST_CONNECT_WS_ENDPOINT` (line 54):
```yaml
      - PW_TEST_CONNECT_WS_ENDPOINT=ws://browser:3000/
```

Change `depends_on` (lines 72-74):
```yaml
    depends_on:
      - browser
```

Change the `default` network comment (line 69):
```yaml
      - default   # instance-scoped Compose network (browser sidecar, project services)
```

- [ ] **Step 3: Verify compose template is valid**

Run: `CSPACE_CONTAINER_NAME=test CSPACE_INSTANCE_NAME=mercury CSPACE_PROJECT_NAME=myproject CSPACE_IMAGE=test CSPACE_HOME=/tmp CSPACE_MEMORY_VOLUME=test CSPACE_LOGS_VOLUME=test CSPACE_PROJECT_NETWORK=test docker compose -f lib/templates/docker-compose.core.yml config 2>&1 | head -20`
Expected: Valid YAML output with `browser` service.

- [ ] **Step 4: Commit**

```bash
git add lib/templates/docker-compose.core.yml
git commit -m "Replace playwright + chromium-cdp sidecars with single browser service"
```

---

### Task 2: Register browser MCP servers in init-claude-plugins.sh

**Files:**
- Modify: `lib/scripts/init-claude-plugins.sh`

- [ ] **Step 1: Add built-in MCP registration**

In `lib/scripts/init-claude-plugins.sh`, add the following block BEFORE the `touch "$MARKER_FILE"` line (around line 157) and AFTER the custom MCP servers block (after line 155):

```bash
# --- Built-in browser MCP servers ---
# Always registered. Both run inside the browser sidecar via docker exec,
# which has unrestricted network access (no firewall). The agent container
# communicates with them over stdio through the docker exec pipe.
BROWSER_CONTAINER="${CSPACE_CONTAINER_NAME}.browser"
if [ -n "$CSPACE_CONTAINER_NAME" ]; then
    echo "Registering browser MCP servers..."

    # Playwright MCP — browser automation via the sidecar's Chrome instance
    echo "  - playwright: registering"
    claude mcp add --scope user playwright -- \
        docker exec -i "$BROWSER_CONTAINER" \
        npx --yes @playwright/mcp@latest \
        --cdp-endpoint http://localhost:9222 --no-sandbox 2>&1 | sed 's/^/      /' || true

    # Chrome DevTools MCP — page inspection via CDP
    echo "  - chrome-devtools: registering"
    claude mcp add --scope user chrome-devtools -- \
        docker exec -i "$BROWSER_CONTAINER" \
        npx --yes chrome-devtools-mcp@latest \
        --browserUrl http://localhost:9222 2>&1 | sed 's/^/      /' || true
fi
```

- [ ] **Step 2: Commit**

```bash
git add lib/scripts/init-claude-plugins.sh
git commit -m "Auto-register browser MCP servers via docker exec into browser sidecar"
```

---

### Task 3: Update orphan container cleanup in provision.go

**Files:**
- Modify: `internal/provision/provision.go:79`

- [ ] **Step 1: Update the suffix list**

In `internal/provision/provision.go`, find line 79:

```go
for _, suffix := range []string{"", ".playwright", ".chromium-cdp"} {
```

Change to:

```go
for _, suffix := range []string{"", ".browser"} {
```

- [ ] **Step 2: Run tests and vet**

Run: `make test && make vet`
Expected: All pass.

- [ ] **Step 3: Commit**

```bash
git add internal/provision/provision.go
git commit -m "Update orphan cleanup for consolidated browser sidecar"
```

---

### Task 4: Build, sync embedded assets, and integration test

**Files:**
- No new files — verification step.

- [ ] **Step 1: Build**

Run: `make build`
Expected: Build succeeds with embedded assets synced.

- [ ] **Step 2: Run tests**

Run: `make test && make vet`
Expected: All pass.

- [ ] **Step 3: Integration test**

```bash
# Tear down existing test instance
cd ~/Projects/resume-redux && cspace down venus

# Start fresh instance with new binary
cspace up venus --no-claude
```

After provisioning, verify:

```bash
# Verify browser sidecar is running
docker ps --filter name=re-venus.browser --format '{{.Names}} {{.Status}}'

# Verify old sidecars are NOT running
docker ps --filter name=re-venus.playwright --format '{{.Names}}'
docker ps --filter name=re-venus.chromium-cdp --format '{{.Names}}'

# Verify Chrome CDP is accessible inside browser sidecar
docker exec re-venus.browser curl -s -H "Host: localhost" http://localhost:9222/json/version

# Verify Playwright run-server is accessible
docker exec re-venus curl -s -o /dev/null -w "%{http_code}" http://browser:3000

# Verify MCP servers are registered
docker exec -u dev re-venus claude mcp list

# Verify browser sidecar has unrestricted network access
docker exec re-venus.browser curl -s -o /dev/null -w "%{http_code}" https://example.com

# Verify agent container is still firewalled
docker exec re-venus curl -s --connect-timeout 3 -o /dev/null -w "%{http_code}" https://example.com
# Expected: 000 (connection refused/timeout)

# Verify hosts injection includes browser sidecar
docker exec re-venus.browser grep cspace.local /etc/hosts
```

- [ ] **Step 4: Run E2E tests**

```bash
docker exec -u dev -w /workspace re-venus pnpm run e2e 2>&1 | tail -10
```

Expected: E2E tests pass (Playwright connects to `ws://browser:3000/`).
