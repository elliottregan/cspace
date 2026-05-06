# Image dependency contract

cspace's runtime overlay (the supervisor, init scripts, plugin install
machinery) is bind-mounted into your sandbox image at `/opt/cspace/`.
For the overlay to function, the image must provide:

| Dependency | Why | Default image (node:24-bookworm-slim) |
|---|---|---|
| **glibc** | Supervisor is a Bun-compiled binary linked against glibc | ✓ |
| **iptables** | Loopback NAT for ports bound to 127.0.0.1 inside the sandbox | ✓ (auto-installed via apt if missing on debian/ubuntu) |
| **dnsmasq** | DNS forwarder (sibling-service hostnames + cspace2.local) | ✓ (auto-installed via apt if missing on debian/ubuntu) |
| **bash** | Entrypoint is a bash script | ✓ |
| **tini** (recommended) | PID 1 reaping; cspace's entrypoint will work without it but zombies accumulate | ✓ |
| **sudo** (recommended) | Init script drops from root to your `remoteUser` after privileged setup | ✓ |
| **curl, git** | Plugin installation, marketplace clone refresh | ✓ |

## Default image

If your `.devcontainer/devcontainer.json` doesn't set `image` or `dockerFile`,
cspace uses **node:24-bookworm-slim**. It's chosen because:

- Debian bookworm = glibc, easy `apt-get`.
- Node 24 + npx is enough to run most MCP servers (context7, playwright-mcp, etc.).
- ~250 MB base, comfortable for the overlay model.
- Stable upstream LTS as of 2026.

The default does **not** include project tooling (pnpm 10 global, Go,
Rust, Playwright browsers, etc.). Pin yours via:

- A custom `image` in devcontainer.json, OR
- A `dockerFile` in devcontainer.json (built via Apple Container's native builder), OR
- Devcontainer features (`ghcr.io/devcontainers/features/python:1`, …),
  see [docs/devcontainer-subset.md](./devcontainer-subset.md) for which
  features cspace ships built-in.

## Using a non-default image

cspace will run any glibc-based image that meets the contract. If yours
doesn't ship `iptables`/`dnsmasq`, the entrypoint auto-installs them when
`apt-get` is available. Otherwise, install them yourself:

```dockerfile
# Debian/Ubuntu
RUN apt-get update && apt-get install -y iptables dnsmasq

# Fedora/RHEL/UBI
RUN dnf install -y iptables nftables dnsmasq

# Alpine: NOT SUPPORTED in v1.0 — supervisor is glibc-linked.
```

## What goes wrong if a dep is missing

- **glibc absent (e.g., alpine):** supervisor fails to start with the
  dynamic linker error "no such file or directory". Use a glibc base.
- **iptables absent and apt-get unavailable:** ports bound to 127.0.0.1
  inside the sandbox aren't reachable from sibling services or host
  browsers. Install iptables for your distro.
- **dnsmasq absent and apt-get unavailable:** bare-name service DNS
  (`http://convex-backend:3210`) and `*.cspace2.local` resolution fail.
  Install dnsmasq for your distro.
- **bash absent:** entrypoint can't run. Use an image with bash; busybox
  alone isn't enough because the entrypoint relies on bash builtins.

## Alpine support

Alpine images are not supported in v1.0 because cspace's Bun-compiled
supervisor binary is glibc-linked. A musl supervisor build is planned
for v1.1. Until then, choose a glibc-based image (debian, ubuntu, ubi,
rocky, etc.).
