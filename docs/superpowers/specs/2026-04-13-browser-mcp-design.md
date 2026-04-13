# Consolidated browser sidecar with auto-registered MCP servers

**Date:** 2026-04-13
**Status:** Approved

## Problem

Browser MCP servers (Chrome DevTools, Playwright) don't work out of the box in cspace. Projects must manually create wrapper scripts with CDP Host header proxies and register MCPs in `.mcp.json`. Additionally, the agent container is firewalled — MCP browsers need unrestricted network access to reach external URLs (deployed sites) and container-hosted sites.

## Design

### Consolidated browser sidecar

Replace the two sidecars (`playwright` + `chromium-cdp`) with a single `browser` sidecar using `mcr.microsoft.com/playwright`. The sidecar:

- Starts headless Chrome on port 9222 (CDP endpoint)
- Starts Playwright run-server on port 3000 (for E2E test execution)
- Has Node.js available for running MCP servers via `docker exec`
- Joins both `default` and `project` networks (reachable by Traefik, receives hosts injection)
- Has unrestricted network access (no firewall)

Container name: `${CSPACE_CONTAINER_NAME}.browser`

The entrypoint launches Chrome and Playwright run-server as background processes.

### MCP registration

`init-claude-plugins.sh` registers both MCPs as built-in (always-on) using `docker exec` to run inside the browser sidecar:

**Playwright MCP:**
```
docker exec -i ${CSPACE_CONTAINER_NAME}.browser npx @playwright/mcp@latest --cdp-endpoint http://localhost:9222 --no-sandbox
```

**Chrome DevTools MCP:**
```
docker exec -i ${CSPACE_CONTAINER_NAME}.browser npx chrome-devtools-mcp@latest --browserUrl http://localhost:9222
```

Both connect to Chrome on `localhost:9222` inside the sidecar — no Host header proxy needed since the MCP process and Chrome are in the same container.

### Agent container changes

- `PW_TEST_CONNECT_WS_ENDPOINT` changes from `ws://playwright:3000/` to `ws://browser:3000/`
- `depends_on` changes from `[playwright, chromium-cdp]` to `[browser]`

### Network access

The browser sidecar has unrestricted network access (no iptables firewall). This means MCP browsers can:
- Navigate to external URLs (deployed sites, documentation)
- Access container-hosted sites via `cspace.local` hostnames (hosts injection)
- Reach Convex and other project services on the Docker network

The agent container remains firewalled — only the browser sidecars bypass it.

## Files changed

- `lib/templates/docker-compose.core.yml` — replace `playwright` + `chromium-cdp` with `browser` service
- `lib/scripts/init-claude-plugins.sh` — add built-in MCP registration
- `internal/provision/provision.go` — update orphan container cleanup suffix list
