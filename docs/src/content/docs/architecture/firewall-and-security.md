---
title: Firewall & Security
description: The iptables-based network firewall, domain allowlisting, and security model of cspace devcontainers.
sidebar:
  order: 4
---

Each cspace devcontainer runs an iptables-based egress firewall that restricts outbound network traffic to an explicit allowlist. This prevents autonomous agents from accessing arbitrary internet services — they can only reach GitHub, npm, Anthropic's API, and domains you explicitly approve.

The firewall is implemented in `lib/scripts/init-firewall.sh` and runs during container startup.

## Firewall model

The firewall uses a **default-deny** policy with allowlisted exceptions:

- **All outbound traffic is blocked** unless it matches the allowlist
- Blocked connections get an immediate **REJECT** (not a silent DROP), so tools fail fast with a clear error rather than hanging on a timeout
- **Inbound traffic** is restricted to established connections and Docker network traffic
- **Forwarding** is disabled entirely

## Initialization sequence

The firewall script runs on every container start (iptables rules are kernel state and don't persist across container restarts). The initialization follows a careful sequence to avoid locking the container out of the network:

1. **Extract Docker DNS rules** — save internal Docker DNS resolution rules before flushing
2. **Set permissive policies** — temporarily allow all traffic to prevent lockout during rule changes
3. **Flush all existing rules** — clean slate across all iptables tables (filter, nat, mangle)
4. **Restore Docker DNS** — re-apply the saved DNS resolution rules so container DNS keeps working
5. **Build the allowlist** — resolve domains, fetch GitHub IP ranges, add CIDR blocks
6. **Set restrictive policies** — switch to default-deny with the allowlist in place
7. **Verify** — confirm blocked sites are blocked and allowed sites are reachable

## What's allowed

### Always allowed (base services)

These are required for Claude Code and core tooling to function:

| Category | Domains |
|----------|---------|
| **DNS** | UDP port 53 (all DNS resolution) |
| **SSH** | TCP port 22 (git operations over SSH) |
| **Localhost** | Loopback interface (internal communication) |
| **npm** | `registry.npmjs.org` |
| **Anthropic** | `api.anthropic.com`, `auth.anthropic.com` |
| **Claude** | `claude.ai`, `context7.com` |
| **Observability** | `sentry.io`, `us.sentry.io`, `mcp.sentry.dev`, `statsig.anthropic.com`, `statsig.com` |
| **Testing** | `playwright.dev` |
| **Vercel CDN** | `76.76.21.0/24`, `64.29.17.0/24`, `216.198.79.0/24` |

### GitHub IP ranges

GitHub IPs are fetched dynamically from the GitHub meta API (`https://api.github.com/meta`) and include the `web`, `api`, and `git` CIDR ranges. This covers:

- `github.com` web interface
- GitHub API (`api.github.com`)
- Git operations over HTTPS
- GitHub Actions artifact storage

The script validates the API response before adding ranges. If the fetch fails, the firewall script exits with an error — a firewall without GitHub access is not useful for agents.

### Docker network traffic

The firewall auto-detects all connected Docker subnets and allows traffic to and from them. This ensures:

- Services within the instance (postgres, redis, etc.) are reachable
- The host gateway network is accessible
- Browser sidecars (Playwright, Chromium CDP) can communicate with the devcontainer

### Project-specific domains

Additional domains are read from the `CSPACE_FIREWALL_DOMAINS` environment variable (space-separated). This variable is set from the `firewall.domains` array in `.cspace.json`:

```json
{
  "firewall": {
    "domains": ["api.example.com", "cdn.example.com"]
  }
}
```

Domains are resolved to IP addresses via DNS at firewall initialization time. If a domain fails to resolve, a warning is logged but the firewall continues.

:::caution
Domain resolution happens once at container startup. If a service's IP addresses change after the firewall initializes, the container may lose access until it restarts.
:::

## Domain resolution

The firewall uses `ipset` with `hash:net` to build a CIDR-based allowlist. Domains are resolved using two methods:

- **CIDR ranges** (GitHub IPs, Vercel CDN) — added directly to the ipset
- **Domain names** — resolved to IP addresses via `dig +short A`, then each IP is added individually

This means the allowlist is IP-based, not hostname-based.

## Verification

After building the allowlist, the firewall script runs two verification checks:

1. **Blocked test** — confirms `https://example.com` is unreachable
2. **Allowed test** — confirms `https://api.github.com/zen` is accessible

If either check fails, the script exits with an error and the container will not start successfully. A marker file (`/tmp/.firewall-init-done`) is created on success.

## Customizing the firewall

### Adding project domains

The most common customization is adding domains your project needs (APIs, CDNs, package registries):

```json
{
  "firewall": {
    "domains": [
      "api.stripe.com",
      "fonts.googleapis.com",
      "registry.yarnpkg.com"
    ]
  }
}
```

### Disabling the firewall

If you don't want network restrictions (e.g., during initial setup or debugging):

```json
{
  "firewall": {
    "enabled": false
  }
}
```

### Overriding the firewall script

For advanced customization, you can override the entire firewall script by placing a custom version at `.cspace/` and updating the Dockerfile. Since the firewall script is copied into the container image at build time, overriding it requires a custom Dockerfile:

1. Place your custom firewall script at `.cspace/init-firewall.sh`
2. Create a `.cspace/Dockerfile` that extends the base and copies your script:

```dockerfile
FROM cspace-dev
COPY .cspace/init-firewall.sh /usr/local/bin/init-firewall.sh
RUN chmod +x /usr/local/bin/init-firewall.sh
```

## Security model

### Container isolation

Each devcontainer instance is isolated from others:

- **Separate filesystem** — each instance has its own workspace volume
- **Separate network** — each instance runs on its own Docker network
- **Separate credentials** — each instance has its own GitHub CLI config
- **No cross-instance access** — instances cannot reach each other's services

The only shared state is the agent memory and logs volumes, which are read/write for all instances.

### Authentication

- **GitHub token** — passed via `GH_TOKEN` in the project `.env` file, loaded into the container environment
- **No SSH agent access** — containers use HTTPS for all git operations
- **Anthropic API key** — passed through the environment for Claude Code

### Capabilities

The devcontainer requests two Linux capabilities:

- `CAP_NET_ADMIN` — required for `iptables` to configure the firewall
- `CAP_NET_RAW` — required for raw socket operations used by the firewall

No other elevated capabilities are granted. The `dev` user has passwordless `sudo` for convenience during development, but the container runs as a non-root user by default.

### Docker socket access

:::caution
The host Docker socket is mounted read-write. This is the most privileged access the container has — a process with Docker socket access can effectively control the host. This is an intentional design choice: agents need to spawn sibling containers (the Docker-outside-Docker pattern). If this is a concern for your environment, consider running cspace on a dedicated machine or VM.
:::
