# cspace

Portable CLI for managing isolated Claude Code devcontainer instances. Spin up multiple independent development environments, each with their own workspace, browser sidecars, and network firewall — then run autonomous Claude agents against GitHub issues.

## Quick Start

```bash
# Install
curl -fsSL https://raw.githubusercontent.com/elliottregan/cspace/main/install.sh | bash
source ~/.zshrc  # or restart your shell

# Initialize in a project
cd my-project
cspace init

# Launch an instance
cspace up
```

## What It Does

- **Multi-instance management** — Run multiple isolated devcontainers in parallel, each with their own workspace and state. Instances auto-assign planet names (mercury, venus, earth...) with deterministic port mappings.
- **Claude Code integration** — Each instance has Claude Code pre-installed with configurable model, effort level, hooks for progress logging, and transcript export.
- **Browser sidecars** — Shared Playwright run-server and headless Chromium (CDP) containers for E2E testing and browser automation via MCP.
- **Network firewall** — iptables-based allowlist restricts egress to GitHub, npm, Anthropic, and your project-specific domains.
- **Autonomous agents** — Run `cspace issue 42` to fully autonomously resolve a GitHub issue: explore code, design architecture, implement, verify, and ship a PR.
- **Multi-agent coordination** — The coordinator prompt manages parallel agents across issues with dependency tracking and merge ordering.

## Prerequisites

- **Docker** with Docker Compose v2
- **jq** for JSON processing
- **gum** (optional) for interactive TUI — `brew install gum`
- **gh** (optional) for GitHub integration — `brew install gh`
- **`GH_TOKEN`** in your project `.env` — required for git push/pull from inside the container. See [Git Authentication](#git-authentication) below.

## Git Authentication

Containers have no access to your host's SSH agent, so all git operations go over HTTPS using a GitHub personal access token. cspace wires this up automatically — you just need to provide the token.

### Setup (one-time per project)

1. **Create a token** with the right scopes:

   ```
   https://github.com/settings/tokens/new?scopes=repo,workflow,read:org
   ```

   Required scopes:
   - **`repo`** — read/write to repository contents (clone, push, open PRs)
   - **`workflow`** — required if agents will edit `.github/workflows/` files
   - **`read:org`** — required for SSO-protected org repos and `gh` org commands

   For org repos with SAML SSO: after creating the token, click **"Configure SSO"** next to it and authorize each org you need access to.

2. **Add it to your project `.env`**:

   ```bash
   echo 'GH_TOKEN=ghp_yourTokenHere' >> .env
   ```

   (cspace's `.env` loading is project-local — don't commit this file.)

3. **`cspace up`** — instance creation will:
   - Run `gh auth setup-git` to register `gh` as a git credential helper
   - Rewrite any `git@github.com:` SSH URLs to `https://github.com/` automatically
   - Alias `GITHUB_TOKEN=$GH_TOKEN` (and vice versa) so tools expecting either name work

If `GH_TOKEN` is missing, instance creation **fails loudly** with a setup hint — agents would otherwise hang on credential prompts.

### What works after setup

- `git push` / `git pull` against `origin`
- `gh pr create`, `gh issue list`, etc.
- MCP servers and tooling that read `GITHUB_TOKEN`
- Autonomous agents pushing feature branches and opening PRs

### Limitations

- **No commit signing.** Repos that require GPG/SSH-signed commits will reject agent pushes. Workaround: relax the rule for bot accounts, or open an issue to add signing support.
- **No automatic token refresh.** Rotating `GH_TOKEN` on the host requires recreating instances (`cspace down <name> && cspace up <name>`).
- **Branch protection.** Direct pushes to protected branches (typically `main`) are blocked — agents should always push feature branches and open PRs. The built-in implementer prompt does this by default.

## Commands

```
cspace                              Interactive TUI menu
cspace init                         Scaffold .cspace.json + .cspace/
cspace init --full                  Also copy templates for customization
cspace up [name|branch]             Create instance + launch Claude
cspace up --no-claude [name]        Create instance only
cspace issue <num> [name]           Autonomous agent for a GitHub issue
  --base <branch>                   Base branch (default: main)
  --prompt "..."                    Additional instructions
cspace resume <name> <session-id>   Resume a previous session
cspace ssh <name>                   Shell into instance
cspace list                         List running instances for this project
cspace list --all                   List instances across ALL projects
cspace ports <name>                 Show port mappings
cspace down <name>                  Destroy instance + volumes
cspace down --all                   Destroy this project's instances + sidecars
cspace down --everywhere            Destroy ALL instances across ALL projects
cspace warm <name...>               Pre-provision containers
cspace shared up|down               Manage browser sidecars
cspace rebuild                      Rebuild container image
cspace sync-context                 Generate milestone context
cspace self-update                  Update to latest version
```

## Configuration

Each project has a `.cspace.json` in the root:

```json
{
  "project": {
    "name": "my-project",
    "repo": "owner/my-project",
    "prefix": "mp"
  },
  "container": {
    "ports": { "3000": "dev server", "4173": "preview server" },
    "environment": { "VITE_HOST": "0.0.0.0" }
  },
  "firewall": {
    "enabled": true,
    "domains": ["api.example.com"]
  },
  "claude": {
    "model": "claude-opus-4-6[1m]",
    "effort": "max"
  },
  "verify": {
    "all": "npm run lint && npm run test",
    "e2e": "npm run e2e"
  },
  "agent": {
    "issue_label": "ready"
  },
  "services": ".cspace/docker-compose.yml",
  "post_setup": ".cspace/post-setup.sh"
}
```

### Config Merging

Configuration is merged in order (last wins):

1. `$CSPACE_HOME/lib/defaults.json` — built-in defaults
2. `.cspace.json` — project config (committed to git)
3. `.cspace.local.json` — local overrides (gitignored)

### Auto-detection

If not specified in config:
- **project.name** — derived from directory name
- **project.repo** — detected from `git remote`
- **project.prefix** — first 2 characters of project name

## Project-Specific Services

Add extra Docker Compose services (databases, backends, etc.) in `.cspace/docker-compose.yml`:

```yaml
services:
  # Extend the devcontainer with project-specific env vars
  devcontainer:
    environment:
      - DATABASE_URL=postgresql://dev:dev@postgres:5432/myapp

  postgres:
    image: postgres:16
    container_name: ${CSPACE_PREFIX}.${COMPOSE_PROJECT_NAME}.postgres
    environment:
      POSTGRES_DB: myapp
      POSTGRES_USER: dev
      POSTGRES_PASSWORD: dev
    volumes:
      - postgres-data:/var/lib/postgresql/data
    networks:
      - default

volumes:
  postgres-data:
```

Point to it in `.cspace.json`:
```json
{
  "services": ".cspace/docker-compose.yml"
}
```

## Post-Setup Hook

Run project-specific initialization after the container is created. Add a script at `.cspace/post-setup.sh`:

```bash
#!/bin/bash
# Example: set up a database
set -euo pipefail

if [ -f /workspace/.cspace-db-done ]; then exit 0; fi

echo "Running database migrations..."
cd /workspace && npm run migrate

touch /workspace/.cspace-db-done
```

Point to it in `.cspace.json`:
```json
{
  "post_setup": ".cspace/post-setup.sh"
}
```

## Template Overrides

Override any built-in template by placing a file in `.cspace/`:

| Override path | What it replaces |
|---------------|-----------------|
| `.cspace/Dockerfile` | Container image |
| `.cspace/docker-compose.core.yml` | Core devcontainer service |
| `.cspace/docker-compose.shared.yml` | Browser sidecars |
| `.cspace/agents/implementer.md` | Autonomous agent prompt |
| `.cspace/agents/coordinator.md` | Multi-agent coordinator prompt |

## Architecture

```
Host machine
├── cspace CLI (installed globally)
│
├── Shared services (one set per project)
│   ├── Playwright run-server (ws://playwright:3000)
│   └── Chromium CDP (chromium-cdp:9222)
│
├── Instance: mercury
│   ├── devcontainer (Claude Code, SSH, firewall)
│   ├── [project services from .cspace/docker-compose.yml]
│   └── Volumes: workspace, claude-home, gh-config
│
├── Instance: venus
│   ├── devcontainer
│   ├── [project services]
│   └── Volumes (separate from mercury)
│
└── Shared volumes (across all instances)
    ├── cspace-{project}-memory (Claude agent memory)
    └── cspace-{project}-logs (session transcripts)
```

## Autonomous Agent Workflow

When you run `cspace issue 42`, the agent:

1. **Setup** — Creates branch `issue-42`, opens draft PR
2. **Explore** — Launches parallel code-explorer agents to understand the codebase
3. **Design** — Launches parallel code-architect agents with different trade-offs
4. **Implement** — Picks best approach, writes code
5. **Verify** — Runs your configured lint/typecheck/test/e2e commands
6. **Ship** — Commits, pushes, marks PR ready
7. **Review** — Takes screenshots, runs code review, verifies acceptance criteria

The verification commands come from `verify.all` and `verify.e2e` in your `.cspace.json`.

## License

MIT
