# cspace

Portable CLI for managing isolated Claude Code devcontainer instances. Spin up multiple independent development environments, each with their own workspace, browser sidecars, and network firewall — then run autonomous Claude agents against GitHub issues.

**[Full documentation](https://cspace-cli.netlify.app/)**

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/elliottregan/cspace/main/install.sh | bash
source ~/.zshrc  # or restart your shell
```

Requires **Docker** with Compose v2 and **jq**. See [prerequisites](https://cspace-cli.netlify.app/getting-started/installation/#prerequisites) for optional dependencies.

## Quick Start

```bash
cd my-project
cspace init              # scaffold .cspace.json
echo 'GH_TOKEN=ghp_...' >> .env   # required for git inside containers
cspace up                # launch an instance
```

See the [quick start guide](https://cspace-cli.netlify.app/getting-started/quick-start/) and [git authentication setup](https://cspace-cli.netlify.app/getting-started/git-authentication/) for details.

## Usage

```
cspace                              Interactive TUI menu
cspace up [name|branch]             Create instance + launch Claude
cspace issue <num> [name]           Autonomous agent for a GitHub issue
cspace ssh <name>                   Shell into instance
cspace list                         List running instances
cspace down <name>                  Destroy instance
cspace down --all                   Destroy all project instances
cspace self-update                  Update to latest version
```

Full command reference: [CLI docs](https://cspace-cli.netlify.app/cli-reference/overview/)

## How It Works

```
Host machine
├── cspace CLI (installed globally)
├── Shared services (per project)
│   ├── Playwright run-server
│   └── Chromium CDP sidecar
├── Instance: mercury
│   ├── devcontainer (Claude Code, SSH, firewall)
│   ├── project services (.cspace/docker-compose.yml)
│   └── isolated volumes
├── Instance: venus
│   └── ...
└── Shared volumes
    ├── agent memory
    └── session logs
```

Instances auto-assign planet names (mercury, venus, earth...) with deterministic port mappings. Each instance gets its own workspace, Claude Code installation, and network firewall that restricts egress to an allowlist (GitHub, npm, Anthropic, and your custom domains).

Running `cspace issue 42` launches an autonomous agent that explores the codebase, designs and implements a solution, runs your verification commands, and opens a PR.

More: [architecture overview](https://cspace-cli.netlify.app/architecture/architecture-overview/) | [autonomous agent workflow](https://cspace-cli.netlify.app/architecture/autonomous-agent-workflow/) | [multi-agent coordination](https://cspace-cli.netlify.app/architecture/multi-agent-coordination/)

## Configuration

Project config lives in `.cspace.json` (committed) with optional `.cspace.local.json` overrides (gitignored):

```json
{
  "project": { "name": "my-project", "repo": "owner/my-project" },
  "container": { "ports": { "3000": "dev server" } },
  "firewall": { "domains": ["api.example.com"] },
  "claude": { "model": "claude-opus-4-7[1m]" },
  "verify": { "all": "npm run lint && npm run test" }
}
```

Override built-in templates (Dockerfile, agent prompts, compose files) by placing files in `.cspace/`. Run `cspace init --full` to copy all templates for customization.

Full reference: [configuration](https://cspace-cli.netlify.app/configuration/configuration-reference/) | [project services](https://cspace-cli.netlify.app/configuration/project-services/) | [template overrides](https://cspace-cli.netlify.app/configuration/template-overrides/)

## Development

The CLI is written in Go with Cobra. The agent supervisor is Node.js (ESM).

```bash
# Build
make build
./bin/cspace-go --help

# Test and lint
make test
make vet

# Rebuild the container image after Dockerfile/template changes
cspace rebuild
```

### Project Structure

- `cmd/cspace/` — CLI entry point
- `internal/cli/` — Cobra commands
- `internal/config/` — Three-layer config merging (defaults → `.cspace.json` → `.cspace.local.json`)
- `internal/supervisor/` — Agent supervisor launcher and NDJSON stream processing
- `internal/provision/` — Container provisioning (git bundle, compose up, workspace init)
- `lib/agent-supervisor/` — Node.js agent supervisor (wraps Claude Agent SDK)
- `lib/scripts/` — Container-side init scripts (firewall, plugins, workspace)
- `lib/agents/` — Agent playbooks (implementer, coordinator)
- `lib/templates/` — Dockerfile, docker-compose files

### Adding a CLI Command

Create a `newXxxCmd()` function in a new file under `internal/cli/`, returning a `*cobra.Command`. Register it via `root.AddCommand()` in `root.go`.

### Releases

Releases are automated via GoReleaser. Push a tag to trigger a build:

```bash
git tag v0.1.0
git push origin v0.1.0
```

## License

MIT
