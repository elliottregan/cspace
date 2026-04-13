# Universal cspace.local connectivity via /etc/hosts injection

**Date:** 2026-04-13
**Status:** Approved

## Problem

`cspace.local` hostnames resolve to `127.0.0.1` everywhere (via CoreDNS). This works from the host (Traefik listens on `127.0.0.1:80`) but not from inside containers (nothing listens on `127.0.0.1:80` inside a container). This means the same URL can't be used by both the host browser and container-side clients (Playwright browser, client-side JS making API calls).

The concrete impact: a client app loaded via `mercury.resume-redux.cspace.local` tries to connect to `convex.mercury.resume-redux.cspace.local` for its database. This works from the host browser but fails from the Playwright sidecar during E2E tests.

## Design

### /etc/hosts injection

During provisioning, after Traefik starts and connects to the project network, cspace resolves Traefik's IP on the project network and injects `/etc/hosts` entries into all containers in the compose stack.

The entries map `cspace.local` hostnames to Traefik's Docker IP:

```
192.168.x.x  mercury.resume-redux.cspace.local
192.168.x.x  preview.mercury.resume-redux.cspace.local
192.168.x.x  convex.mercury.resume-redux.cspace.local
```

This makes `cspace.local` URLs work from both contexts:
- **Host**: resolves via CoreDNS → `127.0.0.1` → Traefik on port 80
- **Containers**: resolves via `/etc/hosts` → Traefik's Docker IP → Traefik on port 80

### How Traefik's IP is resolved

```
docker inspect cspace-proxy \
  --format '{{(index .NetworkSettings.Networks "<project-network>").IPAddress}}'
```

The IP is dynamic (changes on restart) but refreshed on every `cspace up`.

### Which containers get injected

All containers in the compose stack: the cspace container, playwright sidecar, chromium-cdp sidecar, and any project services. Reached via:
- `<composeName>` (cspace container)
- `<composeName>.playwright`
- `<composeName>.chromium-cdp`
- Any additional services from the project compose

### Which hostnames are injected

1. **Core hostnames** (always): `<instance>.<project>.cspace.local` (dev) and `preview.<instance>.<project>.cspace.local`
2. **Project service hostnames**: discovered from Traefik labels on running containers in the compose stack. Any container with `traefik.http.routers.*.rule=Host(...)` gets its hostname extracted and injected.

### Injection mechanism

A `docker exec` call that:
1. Removes previous cspace.local entries: `sed -i '/cspace\.local/d' /etc/hosts`
2. Appends fresh entries: `echo '<ip> <hostnames>' >> /etc/hosts`

Idempotent — safe to re-run on every `cspace up`.

### Provisioning integration

In `provision.go`, after compose up + wait for readiness (step 8), a new step calls `docker.InjectHosts()` which:
1. Resolves Traefik's IP on the project network
2. Builds the hostname list (core + project services from labels)
3. Injects into all containers in the stack

### Project services with Traefik labels

Projects expose services to the host by adding Traefik labels in `.devcontainer/docker-compose.yml`. For Convex:

```yaml
convex-backend:
    labels:
      - "traefik.enable=true"
      - "traefik.docker.network=${CSPACE_PROJECT_NETWORK}"
      - "traefik.http.routers.${CSPACE_CONTAINER_NAME}-convex.rule=Host(`convex.${CSPACE_INSTANCE_NAME}.${CSPACE_PROJECT_NAME}.cspace.local`)"
      - "traefik.http.routers.${CSPACE_CONTAINER_NAME}-convex.entrypoints=web"
      - "traefik.http.routers.${CSPACE_CONTAINER_NAME}-convex.service=${CSPACE_CONTAINER_NAME}-convex"
      - "traefik.http.services.${CSPACE_CONTAINER_NAME}-convex.loadbalancer.server.port=3210"
```

This routes `convex.mercury.resume-redux.cspace.local` → `convex-backend:3210`.

### App simplification (resume-redux, out of scope for cspace)

With Convex routable through Traefik, the app sets `VITE_CONVEX_URL=http://convex.<instance>.<project>.cspace.local` in `.env.local` during post-setup. No runtime URL resolution (`__SELF__`, `__HOST__`), no app-level proxy, no wrapper scripts. The URL is a concrete hostname that works from every context.

## Files changed (cspace)

- `internal/docker/docker.go` — add `InjectHosts()` helper
- `internal/provision/provision.go` — call `InjectHosts()` after readiness check
- `lib/templates/devcontainer/convex-nuxt/docker-compose.yml` — add Traefik labels to convex-backend
- `lib/templates/devcontainer/convex-nuxt/post-setup.sh` — set `VITE_CONVEX_URL` to cspace.local hostname

## Testing

1. `cspace up mercury --no-claude` in resume-redux
2. From host: `curl http://convex.mercury.resume-redux.cspace.local/version` → Convex version JSON
3. From cspace container: `docker exec re-mercury curl http://convex.mercury.resume-redux.cspace.local/version` → same
4. From Playwright sidecar: `docker exec re-mercury.playwright curl http://convex.mercury.resume-redux.cspace.local/version` → same
5. Host browser loads `http://preview.mercury.resume-redux.cspace.local` — app connects to Convex without errors
