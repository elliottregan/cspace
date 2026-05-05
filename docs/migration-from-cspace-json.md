# Migrating from .cspace.json to devcontainer.json

cspace v1.0 adopts the [devcontainer.json](https://containers.dev/) spec
for project orchestration. The `.cspace.json` file remains for cspace-only
concerns (firewall, plugins, advisors, agent label, Claude model). Several
formerly-cspace-only fields are now better expressed via devcontainer.json
or compose. When both files set the same field, devcontainer.json wins
and cspace warns about the duplication.

## Field mapping

| .cspace.json (deprecated) | devcontainer.json equivalent |
|---|---|
| `services` (compose path) | `dockerComposeFile` (and the `service` field naming the workspace service) |
| `container.environment` | `containerEnv` |
| `container.ports` | `forwardPorts` |
| `container.packages` | One of: a custom `image:`, a `dockerFile:` doing `apt-get install`, or `features:` for the supported set |
| `post_setup` (.cspace/init.sh) | `postCreateCommand` (runs once) or `postStartCommand` (runs every boot) |

## Fields staying in .cspace.json

- `firewall` — cspace-only allowlist mechanic.
- `plugins` — Claude plugin install list.
- `advisors` — long-running advisor agents.
- `agent.issue_label` — the GitHub label cspace's agent loop watches.
- `claude.model`, `claude.effort`, `claude.coordinatorModel` — agent runtime settings.
- `mcpServers` — MCP server registration.
- `verify.all`, `verify.e2e` — verification commands.
- `customizations.cspace.*` — when set in devcontainer.json, these can also live there. The two are merged, with devcontainer's values winning.

## Migration example: resume-redux

Before — `.cspace.json` only:

```json
{
  "container": {
    "ports": {"5173": "vite", "4173": "preview"},
    "environment": {"VITE_HOST": "0.0.0.0"}
  },
  "services": ".cspace/docker-compose.yml",
  "post_setup": ".cspace/init.sh"
}
```

After — `.devcontainer/devcontainer.json` + a slimmer `.cspace.json`:

```json
// .devcontainer/devcontainer.json
{
  "name": "resume-redux",
  "dockerComposeFile": "docker-compose.yml",
  "service": "app",
  "forwardPorts": [5173, 4173],
  "containerEnv": {"VITE_HOST": "0.0.0.0"},
  "postCreateCommand": "corepack pnpm install --frozen-lockfile=false"
}

// .cspace.json (cspace-only concerns)
{
  "project": {"name": "resume-redux"},
  "firewall": {"enabled": true, "domains": []},
  "agent": {"issue_label": "ready"}
}
```

## When migration isn't worth it

If your project has no compose sidecars and no postCreateCommand, you can
keep using `.cspace.json` exclusively — cspace continues to work without a
devcontainer.json. Adding devcontainer.json makes sense when:

- You want VS Code Dev Containers / Codespaces to open the same files.
- You're using compose to declare sidecar services.
- You want a portable post-create hook that doesn't require host-side
  shell scripting.
