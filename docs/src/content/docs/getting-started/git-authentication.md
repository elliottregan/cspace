---
title: Git Authentication
description: Configure git authentication for cspace devcontainers.
sidebar:
  order: 3
---

Containers have no access to your host's SSH agent, so all git operations go over HTTPS using a GitHub personal access token. cspace wires this up automatically — you just need to provide the token.

## Token setup

### 1. Create a token

Create a personal access token with the required scopes:

```text
https://github.com/settings/tokens/new?scopes=repo,workflow,read:org
```

Required scopes:

| Scope | Purpose |
|-------|---------|
| **`repo`** | Read/write repository contents — clone, push, open PRs |
| **`workflow`** | Edit `.github/workflows/` files (needed if agents modify CI) |
| **`read:org`** | Access SSO-protected org repos and `gh` org commands |

:::caution[SSO-enabled organizations]
If your org uses SAML SSO, you must also click **"Configure SSO"** next to the token on the [tokens page](https://github.com/settings/tokens) and authorize each org you need access to. Without this step, git operations against org repos will fail with a 403 error.
:::

### 2. Add the token to your project

Add `GH_TOKEN` to the `.env` file in your project root:

```bash title="Project root"
echo 'GH_TOKEN=ghp_yourTokenHere' >> .env
```

:::note
The `.env` file is project-local and should not be committed to git. cspace's `.gitignore` templates exclude it by default.
:::

### 3. Launch an instance

When you run `cspace up`, instance creation automatically:

- Runs `gh auth setup-git` to register `gh` as a git credential helper
- Rewrites any `git@github.com:` SSH URLs to `https://github.com/` automatically
- Aliases `GITHUB_TOKEN=$GH_TOKEN` (and vice versa) so tools expecting either name work

If `GH_TOKEN` is missing from the container environment, instance creation **fails loudly** with a setup hint — agents would otherwise hang on credential prompts.

## What works after setup

- `git push` / `git pull` against `origin`
- `gh pr create`, `gh issue list`, and other `gh` CLI commands
- MCP servers and tooling that read `GITHUB_TOKEN`
- Autonomous agents pushing feature branches and opening PRs

## Limitations

- **No commit signing.** Repos that require GPG/SSH-signed commits will reject agent pushes. Workaround: relax the rule for bot accounts, or open an issue to add signing support.
- **No automatic token refresh.** Rotating `GH_TOKEN` on the host requires recreating instances (`cspace down <name> && cspace up <name>`).
- **Branch protection applies.** GitHub blocks direct pushes to protected branches (typically `main`) — agents should always push feature branches and open PRs. The built-in implementer prompt does this by default.

## Troubleshooting

### "GH_TOKEN is not set" error

If `cspace up` fails with this error, your `.env` file is missing or doesn't contain `GH_TOKEN`:

```bash title="Terminal"
# Verify your .env has the token
grep GH_TOKEN .env

# If missing, add it
echo 'GH_TOKEN=ghp_yourTokenHere' >> .env

# Recreate the instance
cspace down <name> && cspace up <name>
```

### 403 errors on organization repos

Your token is not authorized for SSO. Go to your [tokens page](https://github.com/settings/tokens), click **"Configure SSO"** next to the token, and authorize the organization.

### Token works on host but not in container

Make sure the token is in your project-root `.env` file, not just exported in your shell. The container reads tokens from the `.env` file via Docker Compose's `env_file` directive — shell exports on the host are not forwarded into containers.
