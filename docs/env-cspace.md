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

`.env.cspace` only wins **among `env_file` entries** — specifically, it wins
over `.env` because it's declared later in the `env_file:` list. Beyond that,
here is the actual merge order `cspace up` applies (later steps overwrite
earlier ones on key collision — see `internal/cli/cmd_up.go`):

1. `.cspace/secrets.env` (cspace-delivered secrets) — merged into the env map
   first.
2. Compose service `environment:`, which includes whatever compose-go already
   resolved from `env_file:` entries — i.e. **`.env` and `.env.cspace`** —
   merges next, overwriting same-named keys from step 1.
3. devcontainer.json `containerEnv` merges after that.
4. `cspace up --env KEY=VALUE` merges after step 3.

So, highest to lowest (for non-credential keys): **`--env` > devcontainer
`containerEnv` > compose `env_file` (`.env.cspace` / `.env`) >
`.cspace/secrets.env`**.

**Known issue (credential keys):** several credential passthrough steps
currently run *after* the `--env` merge — host-shell `ANTHROPIC_API_KEY` /
`CLAUDE_CODE_OAUTH_TOKEN` / `GH_TOKEN`, the GitHub 401-fallback, and the
GitHub token family propagation — so for those keys an ambient host value can
silently override an explicit `--env`. The intended contract is "`--env`
always wins"; the gap is tracked in
`.cspace/context/findings/2026-07-16-env-precedence-smeared-env-flag-loses-to-ambient-credentials.md`.
Until it's fixed, don't rely on `--env` to override a credential that's also
set in your host shell.

**Security caveat:** because `.env.cspace` out-ranks `.cspace/secrets.env` in
this order, a project that redeclares one of cspace's own secret key names
(`ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN`,
`GITHUB_PERSONAL_ACCESS_TOKEN`) in `.env.cspace` **silently overrides the
delivered secret** — the sandbox ends up running with whatever `.env.cspace`
set (often blank or stale) instead of the credential cspace loaded from the
keychain/secrets file, with no warning. `.env.cspace` must not redeclare
cspace secret keys.

This ordering is arguably surprising — one might expect a cspace-delivered
secret to always win over a project's committed override file — and is
tracked as a possible follow-up. The above is what ships today, not an
aspiration; don't design around a "secrets always win" assumption.

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
