# Traefik reverse proxy for per-instance hostnames

**Date:** 2026-04-13
**Status:** Approved

## Problem

cspace instances need accessible URLs for dev servers running inside containers. The current approach relies on OrbStack-specific DNS (`*.orb.local`) which doesn't work on other Docker runtimes, or `localhost:PORT` which requires unique host port mappings and breaks when multiple instances serve on the same internal port.

## Design

### Global shared proxy

A Traefik reverse proxy runs as a global shared service on the host, alongside a CoreDNS sidecar for local DNS resolution. Together they provide per-instance hostnames that work on any Docker runtime.

- **Traefik** (`cspace-proxy`): listens on host port 80, auto-discovers containers via Docker labels, routes HTTP requests by hostname
- **CoreDNS** (`cspace-dns`): listens on host port 53, resolves all `*.cspace.local` queries to `127.0.0.1`
- **Image sizes:** Traefik ~50MB, CoreDNS ~50MB
- **Lifecycle:** started on first `cspace up` if not running, left running across `cspace down`. Survives individual project teardowns.

Both containers are defined in a single compose file embedded at `lib/templates/proxy/docker-compose.yml`.

### Hostname pattern

```
<instance>.<project>.cspace.local
```

Examples:
- `mercury.resume-redux.cspace.local` — dev server (port 3000)
- `preview.mercury.resume-redux.cspace.local` — preview server (port 4173)

The first/primary port gets the bare instance subdomain. Additional ports get a `<label>.` prefix. This maps to Docker labels on each container that Traefik reads for routing.

### Docker labels for routing

The `cspace` service in `docker-compose.core.yml` gets Traefik labels using compose environment variables:

```yaml
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.${CSPACE_CONTAINER_NAME}-dev.rule=Host(`${CSPACE_INSTANCE_NAME}.${CSPACE_PROJECT_NAME}.cspace.local`)"
  - "traefik.http.routers.${CSPACE_CONTAINER_NAME}-dev.service=${CSPACE_CONTAINER_NAME}-dev"
  - "traefik.http.services.${CSPACE_CONTAINER_NAME}-dev.loadbalancer.server.port=3000"
  - "traefik.http.routers.${CSPACE_CONTAINER_NAME}-preview.rule=Host(`preview.${CSPACE_INSTANCE_NAME}.${CSPACE_PROJECT_NAME}.cspace.local`)"
  - "traefik.http.routers.${CSPACE_CONTAINER_NAME}-preview.service=${CSPACE_CONTAINER_NAME}-preview"
  - "traefik.http.services.${CSPACE_CONTAINER_NAME}-preview.loadbalancer.server.port=4173"
```

Future ports (e.g., brainstorm servers started by agents) can be added dynamically via `docker label` or a `cspace expose` command — the architecture supports it without Traefik reconfiguration.

### Networking

Traefik needs to reach containers on their project networks. On each `cspace up`:

1. Ensure `cspace-proxy` is running (start the proxy compose stack if not)
2. Connect `cspace-proxy` to the project network: `docker network connect cspace-<project> cspace-proxy` (idempotent)
3. Compose up the instance as normal — Traefik discovers the new container via its labels

On `cspace down --all`:
- Tear down instances and project network as normal
- Leave `cspace-proxy` running (other projects may need it)
- Traefik automatically deregisters routes for removed containers

### DNS setup (one-time, via `cspace init`)

On macOS, `/etc/resolver/cspace.local` directs all `*.cspace.local` queries to `127.0.0.1:53` where CoreDNS listens.

During `cspace init`, if the resolver file doesn't exist, prompt:
```
cspace needs to configure local DNS for *.cspace.local.
This requires sudo (one-time setup).
Set up local DNS? [Y/n]
```

If yes:
```bash
sudo mkdir -p /etc/resolver
sudo tee /etc/resolver/cspace.local <<< "nameserver 127.0.0.1"
```

Subsequent `cspace init` calls detect the file exists and skip.

### CoreDNS configuration

Minimal Corefile embedded alongside the proxy compose file:

```
cspace.local {
    template IN A {
        answer "{{ .Name }} 60 IN A 127.0.0.1"
    }
}
```

All `*.cspace.local` queries resolve to `127.0.0.1`. No per-instance configuration needed.

### Statusline update

The statusline simplifies — always use `cspace.local` hostnames regardless of Docker runtime. Remove OrbStack detection and `docker port` fallback.

For each port in `.cspace.json`'s `container.ports`:
- `"3000": "dev"` → `http://mercury.resume-redux.cspace.local`
- `"4173": "preview"` → `http://preview.mercury.resume-redux.cspace.local`

Hostnames built from `CSPACE_INSTANCE_NAME` and `CSPACE_PROJECT_NAME` (already available in container environment). Per-label colors: green for dev, yellow for preview, cyan for others (already implemented).

## Files changed

- `lib/templates/proxy/docker-compose.yml` — new: Traefik + CoreDNS compose stack
- `lib/templates/proxy/Corefile` — new: CoreDNS config
- `lib/templates/docker-compose.core.yml` — add Traefik routing labels to `cspace` service
- `lib/scripts/statusline.sh` — use `cspace.local` hostnames, remove OrbStack/docker-port logic
- `internal/provision/provision.go` — ensure proxy running, connect proxy to project network
- `internal/cli/init_cmd.go` — add DNS resolver setup prompt
- `internal/docker/docker.go` — add helper for `docker network connect`
