# Devcontainer Adoption — Design

**Status:** Draft, in implementation
**Tracks:** Issue #69
**Worktree:** `../cspace-devcontainer-adoption` (branch `devcontainer-adoption`)

## Goal

Pivot cspace from owning a project's environment image to running projects authored against the [devcontainer.json](https://containers.dev/) spec. Same `.devcontainer/` directory runs the project in cspace (Apple Container substrate) **and** in VS Code Remote Containers / GitHub Codespaces, with the surface area cspace supports being a **true subset** of the spec — anything outside the subset is hard-rejected with a named error, never silently mistranslated.

Scope is the full pivot in a single push (not phased across releases): compose orchestration, project image as sandbox, runtime overlay, devcontainer reader, parity-mode DNS, canary migration, parity verification.

## Non-goals

- VS Code parity for arbitrary devcontainer features outside our supported subset.
- Alpine / musl support in v1.0 (default image is glibc; non-default is documented "you manage your own deps").
- Distributing pre-built project images to a registry (separate, see #68).
- Replacing Apple Container with a Docker-substrate fallback (Apple Container is the only substrate; no `docker build` shell-out).
- Full devcontainer features registry (we ship direct support for ~6 common ones; registry-driven follows in v1.1).

## Architecture

```
┌────────────────────────────────────────────────────────────────┐
│ Host (macOS)                                                    │
│                                                                 │
│  cspace binary                                                  │
│   ├─ embedded runtime tree → extracted to                       │
│   │  ~/.cspace/runtime/<version>/                               │
│   │   ├─ supervisor (Bun-compiled binary, glibc)                │
│   │   ├─ scripts/ (entrypoint, init, plugin install, dnsmasq)   │
│   │   ├─ marketplace/ (pre-cloned claude-plugins-official)      │
│   │   └─ statusline.sh                                          │
│   │                                                             │
│   ├─ DNS daemon (127.0.0.1:5354 + 192.168.64.1:5354)            │
│   │   → answers <sandbox>.<project>.cspace2.local               │
│   │   → answers <svc>.<sandbox>.<project>.cspace2.local         │
│   │                                                             │
│   └─ /etc/resolver/cspace2.local → 127.0.0.1:5354               │
│                                                                 │
│  ┌──────────────────────────── vmnet bridge 192.168.64.0/24 ───┐│
│  │                                                             ││
│  │  Project's microVM (image from devcontainer.json):          ││
│  │   ┌──────────────────────────────────────────────────────┐  ││
│  │   │ /opt/cspace/  (bind-mounted from host runtime tree)  │  ││
│  │   │   entrypoint.sh ── PID 1                             │  ││
│  │   │     ├─ init NAT, DNS, /etc/hosts injection           │  ││
│  │   │     ├─ install plugins, run init.sh / postCreate     │  ││
│  │   │     └─ exec project's CMD (or claude attach)         │  ││
│  │   │                                                      │  ││
│  │   │ /workspace  (bind-mounted clone)                     │  ││
│  │   │ /home/dev/.claude/...  (bind-mounted sessions/mem)   │  ││
│  │   └──────────────────────────────────────────────────────┘  ││
│  │                                                             ││
│  │  Sibling sidecars (one microVM per compose service):        ││
│  │   ┌─────────────────┐  ┌────────────────────┐               ││
│  │   │ convex-backend  │  │ convex-dashboard   │  ...          ││
│  │   │ (project image) │  │ (project image)    │               ││
│  │   └─────────────────┘  └────────────────────┘               ││
│  │                                                             ││
│  │  Inter-service DNS (parity hinge):                          ││
│  │   /etc/hosts injected into every microVM lists siblings     ││
│  │   by bare service name — matches Docker compose default     ││
│  │   DNS exactly, so project's compose URLs work unchanged.    ││
│  └─────────────────────────────────────────────────────────────┘│
└────────────────────────────────────────────────────────────────┘
```

## The supported subset

### devcontainer.json

| Field | Behavior |
|-------|----------|
| `name` | Cosmetic; surfaced in TUI/logs. |
| `image` | Pulled and used as sandbox image. Default `node:24-bookworm-slim` if absent. |
| `dockerFile` / `build` | Built via the cspace builder (see "Image builds" below). No `docker build` shell-out. |
| `dockerComposeFile` | Path(s) to compose YAML; parsed via `compose-spec/compose-go`. |
| `service` | Compose service that becomes the sandbox. Its image, env, volumes, command apply. |
| `runServices` | Subset of services to spawn (defaults to all defined). |
| `workspaceFolder` | Mount target for `/workspace` clone. Defaults to `/workspace`. |
| `containerEnv` | Env vars set in the sandbox. |
| `mounts` | Extra host bind mounts on the sandbox (`source=…,target=…,type=bind`). |
| `forwardPorts` | Surfaced in `cspace ports` output and statusline; reachable via `<sandbox>.<project>.cspace2.local:<port>` (no host-port-binding step — Apple Container doesn't have one). |
| `portsAttributes` | Cosmetic labels for `cspace ports` output. |
| `postCreateCommand` | Run once after first sandbox boot (replaces `.cspace/init.sh`'s role; init.sh becomes fallback when no devcontainer.json). |
| `postStartCommand` | Run after every sandbox boot. |
| `remoteUser` | User to drop privileges to after entrypoint init. Defaults to `dev`. |
| `features` | Direct support for 6 (see "Features" below); other entries hard-reject. |
| `customizations.cspace.*` | cspace-specific extensions (see "Customizations" below). |

**Hard-rejected** (true-subset enforcement): any field not in the table above. Error message names the field and points to the docs.

### docker-compose

**Supported:**
- `image`, `build`
- `command`, `entrypoint`
- `environment`, `env_file`
- `ports` (informational; surfaced via DNS, not host-port-bound)
- `volumes` (named → host bind, bind mounts → host bind, `:ro` honored)
- `depends_on` (with `condition: service_started | service_healthy | service_completed_successfully`)
- `healthcheck` (`test`, `interval`, `timeout`, `retries`, `start_period`)
- `restart` (`no | always | on-failure | unless-stopped`)
- `working_dir`, `user`, `tty`, `stdin_open`, `init`
- Top-level `volumes:` block (named volume declarations); `external: true` honored.

**Hard-rejected:**
- `networks:` block, `network_mode`, per-service `networks:` field — there is one default network, full stop.
- `cap_add`, `cap_drop`, `privileged`, `devices`, `security_opt` — Apple Container doesn't expose Linux capability tuning.
- `pid`, `ipc`, `userns_mode`, `cgroup_parent` — same.
- `extends`, `profiles` — flatten before authoring.
- `links` (deprecated; bare service-name DNS replaces it).
- `external_links`, `expose` (informational only in compose; ignored).

Each rejection raises a named error (`compose: unsupported field 'networks' in service 'foo'`) with a one-line "why" and a docs link.

## Inter-service DNS — the parity hinge

Docker compose puts every service of a project on a single bridge network with bare-name DNS. A sandbox doing `curl http://convex-backend:3210` Just Works.

We replicate this without per-network DNS by **injecting `/etc/hosts` entries** into every microVM at start. The cspace daemon, having spawned all sidecars and known their vmnet IPs, writes:

```
192.168.64.41  convex-backend
192.168.64.42  convex-dashboard
192.168.64.40  workspace
```

into each microVM's `/etc/hosts` via a one-shot exec. This matches compose's default DNS exactly, so a project's compose-derived URLs (`http://convex-backend:3210/version` in healthchecks, `CONVEX_CLOUD_ORIGIN=http://convex-backend:3210` in env) work without rewriting.

From the **host**, the same services are reachable as `<svc>.<sandbox>.<project>.cspace2.local` (cspace convenience, doesn't affect parity — Docker users would hit `localhost:<forwarded-port>` instead, which we don't emulate because Apple Container has no host-port-binding step).

`CSPACE_SERVICE_<UPPER>_URL` env vars stay (cspace convenience for projects that want a stable env-var name).

## Runtime overlay

cspace's runtime tree (supervisor, scripts, plugin install machinery, marketplace clone) is **bind-mounted** into every microVM at `/opt/cspace/`, regardless of which image the project picked. This decouples cspace upgrades from project image rebuilds.

**On host side:**
- cspace binary embeds the runtime tree (existing `internal/assets` go:embed pattern).
- On first run / on `cspace image build` / on cspace upgrade detection, contents extracted to `~/.cspace/runtime/<cspace-version>/`.
- Older versions kept until pruned by `cspace runtime prune` (rare).

**Inside microVM:**
- `/opt/cspace/` bind-mounted from `~/.cspace/runtime/<active-version>/`.
- Container CMD overridden to `/opt/cspace/entrypoint.sh` (PID 1).
- Entrypoint: NAT init, DNS forwarder (dnsmasq), `/etc/hosts` settle (cspace daemon writes this from outside), plugin install via `/opt/cspace/scripts/install-plugins.sh`, `postCreateCommand` (first boot) / `postStartCommand` (every boot), then `exec`s the project's `CMD` (or `claude --dangerously-skip-permissions` when launched interactively).

**Image dependency contract:** the project's image must provide:
- glibc (so the Bun-compiled supervisor runs).
- `iptables` (for loopback NAT). Auto-installed via apt on debian/ubuntu lineages if missing; documented as required for other distros.
- `dnsmasq` (DNS forwarder). Same auto-install rule.
- `bash` (entrypoint is bash).
- Standard POSIX userland (`tini`, `sudo`, `procps`-equivalent useful but not strictly required).

Default image (`node:24-bookworm-slim`) ships all of this. Non-default images: project author manages, with a docs page listing what to install.

### Default image rationale

`node:24-bookworm-slim` is chosen because:

- **glibc** — supervisor runs without rebuild.
- **Node + npx** — most agents end up needing it for MCP servers (context7, playwright-mcp, etc.) regardless of project language.
- **Debian apt** — easy `apt-get install` for the few cspace-required deps that aren't baked.
- **~250 MB base** — small enough for the lean-image goal once cspace runtime overlay drops the rest.
- **Stable LTS upstream** — Node 24 is current LTS as of 2026.
- **No project tooling baked** — projects pin their own pnpm/bun/python via devcontainer features or compose service images.

Documentation lists the contract; users on alpine/ubi/scratch/etc. configure as needed.

## Image builds without docker

Apple Container ships `container image build`. Two questions:
1. Does it accept the standard Dockerfile grammar a devcontainer is likely to use?
2. Are there build features (BuildKit cache mounts, etc.) we should care about for v1.0?

**Plan:**
- Try `container image build` for `dockerFile` / `build:` blocks first.
- If a project's Dockerfile fails on Apple Container's builder, the failure is the project's problem to resolve (simplify the Dockerfile or pre-build elsewhere and reference via `image:`). cspace does not silently shell out to Docker.
- If `container image build` is missing or unusable for our needs, **map the devcontainer config to a cspace-native equivalent**: parse Dockerfile-spec into a script that runs against the running microVM (essentially "execute `RUN` lines as setup steps in the project's chosen base image"). This is a fallback explored only if the native builder doesn't work; not a v1.0 commitment.
- BuildKit cache mounts and similar are out-of-scope for v1.0; document.

## Customizations.cspace

Devcontainer spec reserves `customizations.<tool-name>` for tool-specific extensions. cspace uses `customizations.cspace.*`:

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
          "trim": true              // strip trailing whitespace; default true
        }
      ],
      // Resource caps for the workspace microVM
      "resources": { "cpus": 6, "memoryMiB": 8192 },
      // Cspace plugin selection (overlays defaults.json plugins.install)
      "plugins": ["superpowers", "context7"],
      // Firewall allowlist additions
      "firewallDomains": ["api.example.com"]
    }
  }
}
```

VS Code / Codespaces ignore `customizations.cspace`; this is spec-conformant.

## Migration & precedence

- **`.devcontainer/devcontainer.json` present:** authoritative for image, env, services, mounts, post-create, features.
- **Plus `.cspace.json`:** cspace-only concerns continue to live here — `firewall`, `claude` (model/effort), `mcpServers`, `verify`, `agent`, `plugins`, `advisors`. The pre-existing `container.{ports,environment,packages}` and `services` fields become **deprecated and ignored** when devcontainer.json is present, with a one-line warning pointing to the migration guide.
- **No devcontainer.json:** existing `.cspace.json`-only path keeps working unchanged. `.cspace/init.sh` is the post-create equivalent.
- **Both:** devcontainer wins for overlapping concerns; `.cspace.json` retained for cspace-only.

## Features

V1.0 ships direct support for these devcontainer features:

| Feature ID | Behavior |
|------------|----------|
| `ghcr.io/devcontainers/features/node:1` | Install Node version per `version` arg via nvm-style. |
| `ghcr.io/devcontainers/features/python:1` | Install Python via deadsnakes / official Debian package. |
| `ghcr.io/devcontainers/features/common-utils:1` | git, curl, wget, sudo, useradd hardening. |
| `ghcr.io/devcontainers/features/docker-in-docker:1` | Install Docker daemon + CLI inside microVM. |
| `ghcr.io/devcontainers/features/git:1` | Latest git via apt. |
| `ghcr.io/devcontainers/features/github-cli:1` | gh CLI install. |

Implementation: `internal/features/builtin/<id>.sh` shipped scripts, run during entrypoint init phase before `postCreateCommand`. Other feature IDs hard-reject with "feature 'X' not supported in v1.0; submit issue or pre-install in your image."

V1.1 adds the registry-driven path (download tarballs, run `install.sh`).

## File structure

### New packages

- `internal/devcontainer/` — Parser, validator, model.
  - `parse.go` — JSON-with-comments parsing, field validation.
  - `model.go` — Typed config (`Config`, `Customizations`, `ExtractCredential`, `Feature`).
  - `validate.go` — Subset enforcement, named errors.
  - `merge.go` — Precedence resolution against `.cspace.json`.
- `internal/compose/v2/` — Replaces existing `internal/compose/`.
  - `parse.go` — Wraps `compose-spec/compose-go`.
  - `subset.go` — Hard-reject pass over parsed model.
  - `model.go` — cspace-internal compose model (the subset, typed).
  - `resolve.go` — Volume / env / image resolution to substrate calls.
- `internal/orchestrator/` — Lifecycle: spawn sidecars, wait healthchecks, run extractors, write hosts files, tear down.
  - `lifecycle.go` — Up / down / status.
  - `healthcheck.go` — Compose-style healthcheck poller.
  - `hosts.go` — `/etc/hosts` injection.
  - `extract.go` — Credential extraction.
- `internal/features/` — Devcontainer features.
  - `builtin/` — Shipped install scripts.
  - `runner.go` — Sequencing + invocation.
- `internal/runtime/` — Runtime overlay management.
  - `extract.go` — Embed → `~/.cspace/runtime/<version>/`.
  - `prune.go` — Old-version cleanup.

### Modified packages

- `internal/cli/cmd_up.go` — Branch on devcontainer.json presence; new orchestration call.
- `internal/cli/cmd_down.go` — Tear down sidecars.
- `internal/cli/cmd_image.go` — `cspace image build` becomes "extract runtime tree" + "build project image if requested."
- `internal/cli/cmd_daemon.go` — 4-label DNS + `/etc/hosts` writer endpoint.
- `internal/substrate/applecontainer/adapter.go` — `Run()` accepts a "runtime overlay path" arg; mounts at `/opt/cspace/`.
- `internal/config/config.go` — Add deprecation warning for old `services`/`container.*` fields when devcontainer.json present.
- `lib/templates/Dockerfile` — Repurposed: produces `cspace/runtime-default:<version>` image whose **only** content is the runtime overlay tree (so users without their own image get a sensible default sandbox image based on `node:24-bookworm-slim`).
- `lib/scripts/cspace-entrypoint.sh` → moves under `internal/runtime/scripts/entrypoint.sh`, becomes the runtime-overlay entrypoint.

### Deleted

- Image-baking of project tooling (Go toolchain, pnpm 10 global, build-essential, Playwright Chromium browsers) — these go away from the cspace base image. Projects that need them install via features or their own image.

## Testing strategy

- **Unit:** parsers (devcontainer + compose), subset enforcement (every rejected field has a test), volume translation, credential extraction trim/no-trim.
- **Integration (in-repo, no Apple Container):** orchestrator with a stub substrate — verify lifecycle order, healthcheck waits, depends-on serialization, env injection, hosts content.
- **End-to-end (Apple Container):**
  - Hello-world compose with redis sidecar (sanity).
  - resume-redux full canary: `MODE=local pnpm run dev` succeeds, dashboard reachable, admin key extracted, depends-on chain honored.
  - Same canary in VS Code Remote Containers — document divergences.

## Acceptance criteria

1. A project authored with **only** `.devcontainer/devcontainer.json` + optional `docker-compose.yml` and **no `.cspace.json`** runs via `cspace up`.
2. resume-redux's `MODE=local pnpm run dev` works end-to-end (dev server hot-reloads, convex backend serves queries, dashboard usable, admin-key auth flow works).
3. The same `.devcontainer/` files run in VS Code Remote Containers without modification (parity test).
4. Cspace runtime upgrades — `brew upgrade cspace-rc` then `cspace up` on an existing project — pick up new runtime without rebuilding the project image.
5. An unsupported compose field (e.g., `networks:`) hard-rejects with a named error and docs link.
6. Default image is `node:24-bookworm-slim`; image dependency contract documented at `docs/image-dependencies.md`.
7. Existing `.cspace.json`-only projects (no devcontainer.json) continue to work unchanged.

## Open questions

- **Apple Container `image build` Dockerfile compatibility.** Verify against resume-redux's Dockerfile (legacy) before committing to "no docker fallback ever." If incompatible for common cases, evaluate the cspace-native fallback (parse Dockerfile, exec RUN lines in a temp microVM, snapshot to image). Resolved during WS4 spike.
- **Sidecar resource budgets.** Per-service CPU/memory caps via `customizations.cspace.resources` per service? V1.0: single global cap from `customizations.cspace.resources`; per-service is v1.1.
- **Plugin marketplace pre-clone.** Currently in image. Move to `~/.cspace/runtime/<version>/marketplace/` so it lives with the overlay and updates with cspace, not with project images. This is the plan.

## Cross-references

- Issue #69 — this spec is the design.
- Issue #68 — image distribution to ghcr.io. Becomes practical after lean image lands here.
- Issue #70 — TUI plan; orthogonal to this work.
- Findings:
  - `2026-05-05-no-convex-init-equivalent-in-v1` — resolves via compose orchestration + extractCredentials.
  - `2026-05-05-sandbox-image-ships-pnpm-9` — resolves structurally (project picks its own pnpm via image / features).
  - `2026-05-05-sandbox-image-lacks-playwright` — resolves structurally (browser sidecar + projects install own e2e Playwright).
- `docs/superpowers/specs/2026-04-12-devcontainer-auto-detection-design.md` — earlier auto-layering attempt, superseded by this design.
