# `.env.cspace` — project-declared container overrides

`.env.cspace` is a convention (not a cspace-enforced feature) for neutralizing
host/cloud environment variables that leak into a sandbox via a project's own
`.env`, without touching that `.env` or forking behavior for the local,
container-free workflow.

It exists because a project's `.env` is written for the developer's own
machine. Inside a cspace sandbox some of those values are wrong — a cloud
database deployment ID when the sandbox runs a self-hosted backend, a
`localhost` URL that isn't reachable from the browser sidecar, and so on.
`.env.cspace` is where the project declares "when running under cspace,
these values change."

## Wiring

Add a second `env_file` entry to the project's devcontainer compose file,
pointing at a sibling `.env.cspace` next to the existing `.env`:

```yaml
# .devcontainer/docker-compose.yml (project-side)
env_file:
  - path: ../.env          # required: false
  - path: ../.env.cspace   # required: false — later file wins
```

Both entries are `required: false` so the file is optional — a project with
no `.env.cspace` behaves exactly as before. compose-go (the compose
implementation cspace uses to parse `dockerComposeFile`) evaluates `env_file:`
entries in list order and lets a later file's keys override an earlier file's
keys, so anything `.env.cspace` sets replaces the same key from `.env` — for
**every** shell in the container, login or not (unlike a `/etc/profile.d`
hack, which only fires for login shells).

## What goes in it

`.env.cspace` is **project-owned, static, and committed** — the same
lifecycle as `.env.example`, not a secrets file. A project author who knows
their own conflicting vars declares the overrides once:

```bash
# .env.cspace — cspace-mode overrides, committed
CONVEX_DEPLOYMENT=
```

That example blanks a cloud Convex deployment ID that `.env` sets for local
dev, so a self-hosted `convex` CLI inside the sandbox doesn't try to talk to
the cloud deployment. The Convex CLI coerces `CONVEX_DEPLOYMENT=""` to null
and won't re-read the stale value. cspace itself has no Convex-specific
knowledge — it only defines the file convention; the project supplies the
contents.

**cspace never writes per-sandbox dynamic values into `.env.cspace`.** It's a
single file at the repo root shared by every concurrent sandbox for the
project — a per-sandbox write from cspace would race between sandboxes and
dirty the working tree. Values that differ per sandbox (admin keys, the
self-hosted backend URL, the workspace host) continue to ride the existing
`/sessions/extracted.env` channel, not this file.

## Precedence (stated honestly)

There are two separate orders, and conflating them is what produced cspace's
worst credential bug. Non-credential keys merge as they always have; the five
cspace-owned credential keys do not participate in that merge at all.

### Non-credential keys

Later steps overwrite earlier ones (`internal/cli/cmd_up.go`):

1. `.cspace/secrets.env` — arbitrary keys a project or user put there
2. Compose service `environment:`, including whatever compose-go resolved from
   `env_file:` entries — i.e. **`.env` and `.env.cspace`**
3. devcontainer.json `containerEnv`
4. `cspace up --env KEY=VALUE`

Highest to lowest: **`--env` > `containerEnv` > compose `env_file`
(`.env.cspace` / `.env`) > `.cspace/secrets.env`**. `.env.cspace` wins over
`.env` because it is declared later in the `env_file:` list.

### The five cspace-owned credential keys

`ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN`,
`GITHUB_PERSONAL_ACCESS_TOKEN` are resolved by `internal/credentials` and
applied **after** every merge above. Compose `env_file` and `containerEnv` are
ignored for them, unconditionally. Order, highest first:

1. `--env KEY=VALUE`
2. Project Keychain — `cspace-<project>-<KEY>`
3. Global Keychain — `cspace-<KEY>`
4. Legacy `secrets.env` (project file, then user file)
5. Ambient host shell
6. Auto-discovery (`gh auth token`; the host `claude /login` Keychain entry)

`--env` genuinely beats ambient host-shell credentials now, closing the gap
that `2026-07-16-env-precedence-smeared-env-flag-loses-to-ambient-credentials`
tracked.

### What this knowingly breaks

Making the rule absolute means three cases regress. This is deliberate — the
alternative was a fallback rung or a per-project opt-out, both declined in
favour of a rule with no exceptions.

1. **A project whose `.env` was its only credential source loses it.** No
   Keychain entry, no `secrets.env`, no host `gh` login → the sandbox
   previously got a working token via the compose merge and now gets none. The
   `up` summary line reports the key as unresolved.
2. **A project pinning a deliberately narrow token gets the broad one.** The
   remedy is `cspace keychain init --project`, which stores the narrow token as
   `cspace-<project>-<KEY>` where it outranks the global entry. The summary
   line names the winning scope, so the substitution is visible.
3. **A project whose *app code* reads one of the five names gets the user's
   personal credential.** `ANTHROPIC_API_KEY` and `GITHUB_TOKEN` are not
   cspace-invented names; an app billing its own Anthropic account from `.env`
   will instead see the host user's key. **There is no opt-out** — projects in
   this position must rename their app-facing variable.

A quieter change affects anyone with **both** a `secrets.env` entry and a
Keychain entry for the same key: the winner flips from file to Keychain.
`cspace keychain status` shows the new winner.

### Baked env is immutable

Credentials are written into the container at create time and never refreshed.
No host-side change reaches a running sandbox; `cspace down <name> && cspace up
<name>` is the only re-bake path. `cspace doctor` reads each running sandbox's
baked env and reports divergence from current host resolution — host-side
probing alone cannot see it, which is how a dead token once sat behind a green
check.

## Naming caveat

`.env.cspace` intentionally matches the shape of Vite's and Nuxt's
`.env.<mode>` convention (`.env.production`, `.env.staging`, ...). That's
coincidental, not a hook into either tool's mode system — **never run the app
with `--mode cspace`**, or the frontend build tooling will pick up
`.env.cspace` itself and apply it in contexts where that's not intended.
`.env.cspace` is meant exclusively for the compose `env_file:` wiring above,
not the app's own dotenv loading.

## Relationship to `.cspace/secrets.env`

These two files solve different problems and shouldn't be confused:

| File | Owner | Contents | Delivery |
|---|---|---|---|
| `.cspace/secrets.env` | cspace / the developer | cspace-delivered credentials (`ANTHROPIC_API_KEY`, `GH_TOKEN`, ...) | Loaded by the CLI, passed as container env at boot. Gitignored. |
| `.env.cspace` | the project | Project-declared container overrides (neutralizing host/cloud vars) | Loaded by compose's `env_file:` mechanism inside the container build. Committed. |

Avoid reusing one of cspace's own secret key names
(`ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN`,
`GITHUB_PERSONAL_ACCESS_TOKEN`) as a key in `.env.cspace` — per the precedence
note above, `.env.cspace` out-ranks `.cspace/secrets.env`, so reusing one of
these keys silently overrides the delivered secret rather than the other way
around.

## Inert on the local box

Because the compose `env_file:` entries only take effect when the compose
file is actually loaded (i.e., inside a cspace/devcontainer boot),
`.env.cspace` has **zero effect** on the box-native workflow (`pnpm dev` / a
locally-run process with no container). Nothing about the local workflow has
to change to adopt this convention — the file simply sits there unread until
a sandbox boots.

## Reaching the workspace: `$CSPACE_WORKSPACE_HOST`

Every `cspace up` sets `CSPACE_WORKSPACE_HOST` inside the devcontainer,
unconditionally — even with `--no-browser`. Its value is the sandbox's
qualified DNS name, `<sandbox>.<project>.cspace.test`, resolved by cspace's
DNS daemon to the sandbox's current vmnet IP.

Use `$CSPACE_WORKSPACE_HOST` — never the raw container hostname (`$(hostname)`
or similar) — whenever code running inside (or alongside) the sandbox needs an
address for the workspace that's reachable from *outside* the devcontainer's
own network namespace. The raw hostname only resolves inside the devcontainer
itself; the shared browser sidecar and the host both need the qualified name.

The statusline (`lib/runtime/scripts/statusline.sh`) already surfaces this
same FQDN (`${CONTAINER}.${PROJECT}.cspace.test`) next to each listening port,
so `cspace up`'s status output and `$CSPACE_WORKSPACE_HOST` always agree.

### Reaching the browser sidecar

`CSPACE_BROWSER_CDP_URL`, `PLAYWRIGHT_MCP_CDP_ENDPOINT`, and
`PW_TEST_CONNECT_WS_ENDPOINT` now carry the shared browser sidecar's stable
DNS name (`browser.<project>.cspace.test`) instead of its raw vmnet IP, so
they keep working across a sidecar restart. If the sidecar wedges or an agent
tears it down, run `cspace browser status` to check health or `cspace browser
restart` to recover it — both work from the host and from inside a sandbox.

### e2e `baseURL` convention

The `run-server` e2e browser (Playwright) runs **remotely**, in the shared
browser sidecar container — not inside the devcontainer and not on the host.
That means a project's end-to-end test config pointing `baseURL` at
`localhost` is wrong inside cspace: `localhost` from the sidecar's point of
view is the sidecar itself, not the dev server.

Projects should make their Playwright (or equivalent) `baseURL` fall back to
`$CSPACE_WORKSPACE_HOST` when it's set:

```ts
// playwright.config.ts
const port = 4173;
const baseURL = process.env.CSPACE_WORKSPACE_HOST
  ? `http://${process.env.CSPACE_WORKSPACE_HOST}:${port}`
  : `http://localhost:${port}`;

export default defineConfig({
  use: { baseURL },
  // ...
});
```

cspace can't inject this for you — it doesn't know the app's dev-server port —
so this is a project-side default, the same "adapt to cspace when present,
otherwise behave exactly as before" shape as the `.env.cspace` convention
above. Outside a cspace sandbox `CSPACE_WORKSPACE_HOST` is unset, so the
fallback to `localhost` keeps the box-native workflow unchanged.
