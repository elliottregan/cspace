# cspace's devcontainer subset

cspace runs a **true subset** of the [devcontainer.json](https://containers.dev/)
specification and [docker-compose](https://compose-spec.io/). Files authored
within this subset run identically in cspace and in VS Code Dev Containers
(modulo the divergences listed at the bottom of this page). Anything outside
the subset hard-rejects with a named error.

This is intentional. We refuse to silently mistranslate compose semantics
that Apple Container microVMs can't honor (e.g., `network_mode: host`).
Better a clear error today than a mysterious failure in production.

## devcontainer.json — supported fields

| Field | Behavior |
|---|---|
| `name` | Cosmetic; surfaced in TUI and logs. |
| `image` | Pulled and used as the workspace sandbox image. Defaults to `node:24-bookworm-slim` when unset. |
| `dockerFile` / `build` | Built via Apple Container's native builder (no `docker build` shell-out). |
| `dockerComposeFile` | Path(s) to compose YAML; parsed via `compose-spec/compose-go`. |
| `service` | Compose service that becomes the workspace sandbox. |
| `runServices` | Subset of services to spawn (defaults to all defined). |
| `workspaceFolder` | Mount target for the project clone. Defaults to `/workspace`. |
| `containerEnv` | Env vars in the sandbox. |
| `mounts` | Extra host bind mounts on the sandbox. |
| `forwardPorts` | Surfaced in `cspace ports` and statusline; reachable via `<sandbox>.<project>.cspace2.local:<port>`. |
| `portsAttributes` | Cosmetic labels for the ports listing. |
| `postCreateCommand` | Runs once per sandbox lifetime. Replaces `.cspace/init.sh`'s role (init.sh remains a fallback when no devcontainer.json). |
| `postStartCommand` | Runs after every sandbox boot. |
| `remoteUser` | User to drop privileges to after the entrypoint's privileged setup. Defaults to `dev`. |
| `features` | Direct support for the six listed in [#features](#features). Other feature IDs hard-reject. |
| `customizations.cspace.*` | cspace extensions (see [#customizations](#cspace-customizations)). |

### Hard-rejected fields

Any devcontainer.json field not listed above. Examples that show up in real
projects:

- `runArgs` — Apple Container has no equivalent for Docker run flags.
- `shutdownAction` — cspace owns the lifecycle.
- `appPort` — use `forwardPorts`.
- `containerUser` — use `remoteUser`.

The error names the offending field and points back here.

## docker-compose — supported fields

Per service:

- `image`, `build`
- `command`, `entrypoint`
- `environment`, `env_file`
- `ports` (informational; see "Ports access" below)
- `volumes` (named → host bind, bind → host path, `:ro` honored)
- `depends_on` with `condition: service_started | service_healthy | service_completed_successfully`
- `healthcheck` (`test`, `interval`, `timeout`, `retries`, `start_period`)
- `restart` (`no | always | on-failure | unless-stopped`)
- `working_dir`, `user`, `tty`, `stdin_open`, `init`

Top-level:

- `volumes:` block with `external: true` honored.

### Hard-rejected compose fields

- `networks:` block (top-level) and per-service `networks:` — single default
  vmnet bridge only; bare-name DNS via `/etc/hosts` injection replaces
  Docker's per-network DNS.
- `network_mode` — Apple Container microVMs are isolated.
- `cap_add`, `cap_drop`, `privileged`, `devices`, `security_opt` — Apple
  Container does not expose Linux capability tuning.
- `pid`, `ipc`, `userns_mode`, `cgroup_parent` — same reason.
- `extends`, `profiles` — flatten before authoring.
- `links` — bare service-name DNS replaces it (it's deprecated upstream too).

The error names the offending field, the service it appears under, and
links back here.

## Inter-service DNS

Compose's default DNS lets services find each other by bare service name.
cspace replicates this by injecting `/etc/hosts` entries into every microVM
listing all sibling services with their vmnet IPs:

```
192.168.64.41 convex-backend
192.168.64.42 convex-dashboard
192.168.64.40 workspace
```

This means project compose URLs work unchanged — `http://convex-backend:3210`
in a healthcheck or env var resolves correctly inside the sandbox and inside
sibling sidecars. **This is the parity hinge** — without it, projects would
need to rewrite URLs for cspace.

## Ports access

Compose's `ports:` directive expresses host port mapping. Apple Container
microVMs don't share the host's port space the way Docker Desktop does
— each microVM gets its own vmnet IP. cspace surfaces forwarded ports in
two ways:

- **From the host browser:** `http://<sandbox>.<project>.cspace2.local:<port>` —
  resolved by cspace's DNS daemon to the sandbox's vmnet IP.
- **From sibling services inside the sandbox cluster:** `http://<service>:<port>` —
  resolved by the injected `/etc/hosts` entries.

The `ports:` directive itself is informational in cspace. We don't fail
the file when it's present, but we don't bind the host port — the access
pattern above replaces it.

## cspace customizations

`customizations.cspace.*` is the spec-conformant extension point for cspace-
specific features. VS Code and Codespaces ignore unknown customizations
(per spec), so adding cspace customizations doesn't break parity.

```jsonc
{
  "customizations": {
    "cspace": {
      // Run an exec in a sidecar after its healthcheck passes,
      // capture stdout, inject into sandbox env. Sequential, fail-fast.
      "extractCredentials": [
        {
          "from": "convex-backend",
          "exec": ["./generate_admin_key.sh"],
          "env": "CONVEX_SELF_HOSTED_ADMIN_KEY",
          "trim": true
        }
      ],
      "resources": { "cpus": 6, "memoryMiB": 8192 },
      "plugins": ["superpowers", "context7"],
      "firewallDomains": ["api.example.com"]
    }
  }
}
```

| Field | Behavior |
|---|---|
| `extractCredentials` | Runs an exec in a sidecar after healthy, captures stdout, injects as sandbox env var. `trim` defaults to true (strips trailing whitespace). |
| `resources` | Per-sandbox CPU/memory cap. Overrides `.cspace.json` `customizations.cspace.resources` (if any). |
| `plugins` | Replaces the cspace plugin install list for this project. |
| `firewallDomains` | Appends to cspace's firewall allowlist for this project. |

## Features

cspace ships built-in support for six common devcontainer features:

| Feature ID | Notes |
|---|---|
| `ghcr.io/devcontainers/features/node:1` | Installs Node version per `version` arg. |
| `ghcr.io/devcontainers/features/python:1` | Installs Python via apt. |
| `ghcr.io/devcontainers/features/common-utils:1` | git, curl, sudo, useradd hardening. |
| `ghcr.io/devcontainers/features/docker-in-docker:1` | Docker daemon + CLI inside the microVM. |
| `ghcr.io/devcontainers/features/git:1` | Latest git via apt. |
| `ghcr.io/devcontainers/features/github-cli:1` | gh CLI via apt. |

Other feature IDs hard-reject in v1.0. Registry-driven feature support
(downloading arbitrary feature tarballs and running their `install.sh`) is
planned for v1.1. Until then, install via your `image:` or `dockerFile:`
or open an issue if you need a specific feature.

## Image build

When `dockerFile` or `build:` is present, cspace builds via Apple
Container's native `container image build`. There is **no fallback** to
`docker build`. If your Dockerfile uses BuildKit features (cache mounts,
secrets, `--platform`, etc.) that the Apple Container builder doesn't
support, pre-build the image yourself elsewhere and reference it via
`image:` instead. cspace will not silently shell out to Docker.

## Known divergences vs VS Code Remote Containers

_(This section will be filled in after end-to-end parity testing in
Task 32 of the implementation plan.)_

Expected divergences:

- `customizations.cspace.*` is silently ignored by VS Code (spec-conformant).
  Projects relying on `extractCredentials` need a manual fallback when
  opening in VS Code (e.g., a documented step to copy the admin key from
  the convex-backend container).
- Port access pattern differs: cspace uses `<sandbox>.<project>.cspace2.local:<port>`,
  VS Code uses `localhost:<forwardedPort>`. The compose file is identical;
  the URL pattern in your local docs/links may need to vary.

Other divergences are tracked here as they're discovered during canary
testing.
