# Sandbox name resolution reliability + env isolation

- **Status:** Draft (design)
- **Date:** 2026-07-12
- **Builds on:** `2026-06-18-shared-browser-sidecar-design.md`

## 1. Motivation

An agent running many tasks in cspace sandboxes for a Nuxt + self-hosted-Convex
project hit repeated friction:

1. **Stale `CONVEX_DEPLOYMENT`** (a cloud Convex var) present in the container
   shell broke every `convex` CLI command when `CONVEX_SELF_HOSTED_URL` was also
   set.
2. **`convex dev` clobbered `VITE_CONVEX_URL`** in `.env.local` to
   `http://convex-backend:3210`, which the browser sidecar can't resolve.
3. **The browser sidecar couldn't resolve the devcontainer** (`$(hostname)` →
   `net::ERR_NAME_NOT_RESOLVED`); the agent fell back to the raw subnet IP.

Investigation surfaced a fourth, underlying problem:

4. **The `*.cspace.test` DNS daemon dies mid-session**, so the friendly-name
   path silently stops resolving — inside containers *and* on the host — while
   the sandboxes keep running for hours.

Friction #2 has been **fixed app-side** (resume-redux#942: the app ignores a
`VITE_CONVEX_URL` that points at the backend origin and routes all readers
through the sanitized value). That fix depends on the browser being able to
reach the dev-server origin — which is exactly what this spec makes reliable.

**This spec covers #1, #3, and #4.** It makes in-container `.cspace.test`
resolution durable, gives the shared browser correct per-sandbox routing, and
adds a non-invasive way to keep host/cloud env vars out of the sandbox.

## 2. Verified root causes

All references are to the cspace repo at time of writing.

- **Daemon death is a detachment bug, not idle-exit.**
  The idle-exit path is gated on live registry entries, so running sandboxes
  keep the daemon alive (`internal/cli/cmd_daemon.go:163-165`,
  `if err != nil || len(entries) > 0 { continue }`). The real cause:
  `ensureRegistryDaemon` spawns `cspace daemon serve` with its stderr wired to a
  **parent-held pipe** (`c.Stderr = stderrBuf`, `internal/cli/cmd_up.go:1152-1159`)
  and **no `Setsid`/`SysProcAttr`** anywhere in `internal/`. When `cspace up`
  exits (always, and immediately under `--no-attach`), the pipe's read end
  closes; the daemon's next write to fd 2 (it logs on gateway-bind retries,
  listener errors, registry errors) takes `EPIPE` → the Go runtime raises
  `SIGPIPE` on fd 1/2 by design → the daemon dies. Empirically confirmed: after
  `cspace up --no-attach`, no daemon process, `dig @127.0.0.1 -p5354` times out,
  and in-container `.cspace.test` resolves nowhere.

- **The browser sidecar has no static fallback for names.**
  `orchestrator.injectAllHosts` (`internal/orchestrator/lifecycle.go`) writes
  `/etc/hosts` entries into the devcontainer (compose service names +
  `workspace`) but the browser sidecar gets none. `orchestrator.ServiceIPs()`
  (`internal/orchestrator/types.go:72`) and `browser.go`'s `InjectHosts` "second
  pass" exist for this but are **never called** — build-but-unwired plumbing.
  So the browser depends entirely on daemon DNS; when the daemon dies (above),
  it can resolve neither `<sandbox>.<project>.cspace.test` nor the compose
  siblings.

- **The health probes never test the failing path.**
  `internal/cli/probes.go` only probes `127.0.0.1:6280` (HTTP) and
  `127.0.0.1:5354` (host-loopback DNS). Nothing tests the container-facing
  gateway listener (`192.168.64.1:5354`) or resolution *from inside* a
  container — the exact path that fails.

- **`CSPACE_WORKSPACE_HOST` is only set when the browser is enabled.**
  `internal/cli/cmd_up.go:636` sets it inside the browser block, so under
  `--no-browser` the one address agents are meant to use is absent.

- **The cloud env var is baked in by compose, then scrubbed too late.**
  The project's devcontainer compose uses `env_file: .env`, so compose-go folds
  `.env` (including `CONVEX_DEPLOYMENT=dev:...`) into the container's process env
  at create time (`internal/cli/cmd_up.go:282-288`). The project's
  `post-create.sh` comments the var out of `.env` and writes a `/etc/profile.d`
  unset — but that only affects **login** shells, so an agent's non-login tool
  shell still inherits the baked-in value.

## 3. Goals / non-goals

**Goals**

- In-container `.cspace.test` resolution stays reliable for the entire life of
  the sandboxes, not just the minutes after `cspace up`.
- The one shared, ref-counted browser routes each concurrent sandbox's
  navigation to the correct dev-server origin, with real origin isolation.
- A non-invasive, project-owned mechanism to keep conflicting host/cloud env
  vars out of the sandbox — with zero effect on the box-native workflow.
- Failures in the DNS chain are loud, not silent.

**Non-goals**

- Per-sandbox browser sidecars (explicitly rejected — sharing is kept).
- Stopping the Convex CLI's `VITE_CONVEX_URL` clobber (fixed app-side).
- MCP/CDP tab-level isolation between concurrent agents on the one Chromium
  (see §7 — tracked separately, with an existing empirical finding).

## 4. Design

### Part 1 — Daemon survivability + reliable in-container DNS

**1a. Detach the daemon (the core fix).**
Spawn `cspace daemon serve` detached and self-sufficient:

- `SysProcAttr{Setsid: true}` so it survives its spawner and terminal close.
- Redirect its stderr/stdout to `~/.cspace/daemon.log` (append, size-capped),
  **never** a parent-held pipe. `ensureRegistryDaemon` keeps reading only the
  first-startup output (until the port accepts) to surface fast-fail errors,
  then stops holding the pipe.
- **Version-checked reuse:** the `/health` endpoint returns the daemon's cspace
  version; `ensureRegistryDaemon` restarts the daemon when the version differs
  from the running CLI, instead of reusing an older squatting binary.
- **Re-ensure from long-running host commands** (`coordinate`, `send`, and the
  host side of the supervisor loop) call the cheap `ensureRegistryDaemon` probe,
  since agents *inside* containers cannot restart it.

*Decision:* keep the self-spawned model with proper detachment (small, targeted
diff that fixes the actual bug). A launchd `KeepAlive` LaunchAgent — which would
also survive host reboot — is recorded as a **follow-up hardening**, not part of
this change. (The `192.168.64.1:5354` gateway bind still needs the existing
retry loop either way, since that address doesn't exist until the first
container boots.)

**1b. Fail loud, and probe the real path.**

- **Boot-time resolution gate:** after the sandbox (and, when present, the
  browser sidecar) is up, `cspace up` resolves `$CSPACE_WORKSPACE_HOST` *from
  inside* each and warns loudly on failure — the DNS chain is verified end to
  end at the moment it's provisioned.
- Extend `probes.go` / `cspace doctor` to test the gateway listener
  (`192.168.64.1:5354`) and an in-container resolution, not just host loopback.

**1c. Qualified-name routing for the shared browser.**

The shared browser resolves each sandbox's dev-server origin **per navigation**
via its own dnsmasq → `192.168.64.1:5354` → the daemon's registry lookup, using
the qualified `<sandbox>.<project>.cspace.test` name. No per-sandbox browser env
or `/etc/hosts` is needed for routing: routing state lives in the URL each
agent hands to its MCP tool, resolved live. Qualified names also give real
**origin-storage isolation** (cookies / localStorage / service workers are
keyed by origin, and `mercury.<p>.cspace.test` ≠ `venus.<p>.cspace.test`).

Two supporting changes:

- **Registry IP refresh.** The daemon answers from `Entry.IP` written at `up`.
  A restarted sandbox gets a new vmnet IP, and vmnet reassigns freed IPs — so a
  stale entry can resolve to a *different live container*. The daemon must
  refresh the IP on lookup (re-inspect the container) or on container-restart
  events, so a stale answer is never served.
- **Drop the static `/etc/hosts` "insurance" idea, and delete the dead
  plumbing.** Injecting names into the shared browser's `/etc/hosts` is worse
  than relying on DNS: stale entries (after a `kill -9` / reboot / out-of-band
  `container` use) pin a name to an IP vmnet later reassigns — silently
  navigating to the *wrong* backend, which beats an honest `NXDOMAIN` in no way;
  it also has a delete/rewrite race between concurrent `up`/`down`, and Chrome
  reads `/etc/hosts` while the sidecar's dnsmasq is `no-hosts`, so curl/node and
  Chrome would disagree on the same name. The reliability budget is spent on 1a
  and 1b instead. Remove the unused `ServiceIPs()` / `InjectHosts` second-pass
  code (or its misleading docstrings) so it stops implying a feature that isn't
  there.

*Scope of the isolation claim (stated honestly):* qualified names deliver
**routing + origin-storage** isolation only. Tab/CDP-level interference between
concurrent agents on the single Chromium is a separate concern (§7).

**1d. Both browser consumers depend on this — including Playwright e2e.**
The sidecar exposes two endpoints, and *both* browsers run inside the sidecar,
so both need Part 1's reliable resolution of the dev-server origin:

- **Shared CDP** (`CSPACE_BROWSER_CDP_URL`, `:9222`) — the one Chromium the
  agent drives via MCP.
- **`run-server`** (`PW_TEST_CONNECT_WS_ENDPOINT`, `:3000`) — the project's
  `@playwright/test` e2e suite connects here (`playwright.config.ts`
  `connectOptions.wsEndpoint`). `run-server` hands out a **fresh browser
  instance per connection**, so e2e is already isolated and sidesteps the §7
  CDP-tab concern.

Reaching the browser works today (the devcontainer connects to the sidecar by
subnet IP). The gap is the reverse leg: because the e2e browser is remote in
the sidecar, the test `baseURL` must be a **sidecar-resolvable** address of the
dev server — `http://$CSPACE_WORKSPACE_HOST:<port>` — not `localhost` (which is
the sidecar's own loopback). Current state is broken on both counts: the config
falls through to `http://localhost:4173`, and even the right name doesn't
resolve once the daemon dies. Part 1a/1b fixes resolution; the `baseURL`
convention is handled in Part 3.

### Part 2 — Env isolation via `.env.cspace`

A project-owned, static, committed file that the devcontainer compose loads
after `.env`, both optional:

```yaml
# .devcontainer/docker-compose.yml (project-side)
env_file:
  - path: ../.env          # required: false
  - path: ../.env.cspace   # required: false
```

- The project **declares** its cspace-mode overrides there — e.g.
  `CONVEX_DEPLOYMENT=` (blank) to neutralize the cloud var that `env_file: .env`
  drags in. compose-go supports `required: false`, and later env_files win, so
  the blank value overrides `.env` in the container's process env — for **every**
  shell, login or not. This kills friction #1 at the source and lets the
  project's `post-create.sh` `/etc/profile.d` hack be deleted. Verified: the
  Convex CLI coerces `CONVEX_DEPLOYMENT=""` to null and won't re-read it.
- **cspace stays general** — it defines the file convention and (optionally)
  scaffolds an empty one; it does not encode Convex knowledge. The project
  author, who knows their own conflicting vars, owns the contents.
- **cspace does NOT write per-sandbox dynamic values into this file.** It's one
  file at the repo root shared by every concurrent sandbox; per-sandbox writes
  would race and dirty the working tree. Dynamic values (admin keys, the
  self-hosted URL, the workspace host) stay in the existing
  `/sessions/extracted.env` channel.
- **Precedence (stated honestly):** highest *among env_files only*. Compose
  `environment:` (which includes `env_file`-resolved content, i.e. this file),
  devcontainer `containerEnv`, and `--env` still beat it — but
  `.cspace/secrets.env` does **not**; it merges into the env map *before*
  compose `environment:` is applied, so `env_file` content (`.env.cspace`)
  actually out-ranks it. Document this so users don't fight it. (Actual
  shipped order: `env_file` out-ranks `secrets.env`; see
  `docs/env-cspace.md`.)
- **Absent locally → zero effect**, so the box-native workflow (`pnpm dev` with
  no container) is untouched — the core "doesn't enforce its usage" requirement.

**Naming caveat (accepted):** `.env.cspace` matches Vite/Nuxt's `.env.<mode>`
pattern, so running the app with `--mode cspace` would make the *app* load it
too. cspace projects must not use `cspace` as an app build mode. The spec
documents this; the file is intended for compose `env_file`, not the app's own
dotenv. (`.env.cspace` chosen over `.cspace/container.env` per maintainer
preference.) Document its relationship to `.cspace/secrets.env`:
`secrets.env` = cspace-delivered secrets; `.env.cspace` = project-declared
container env overrides.

### Part 3 — Discoverability / docs

- Set `CSPACE_WORKSPACE_HOST` **unconditionally** (move it out of the
  browser-only block at `cmd_up.go:636`) — the qualified name is a useful
  address regardless of the browser, and docs will point at it.
- Publicize `$CSPACE_WORKSPACE_HOST` (statusline, agent guidance) as *the*
  address for reaching the site from the sidecar or host, replacing
  `$(hostname)` (which returns the unresolvable container name). The per-project
  CLAUDE.md guidance that currently says `$(hostname)` should reference the env
  var instead.
- **e2e `baseURL` convention.** Because the `run-server` e2e browser is remote
  in the sidecar (Part 1d), document that a project's Playwright `baseURL` must
  be `http://$CSPACE_WORKSPACE_HOST:<port>` when `CSPACE_WORKSPACE_HOST` is set,
  not `localhost`. cspace can't inject the full `BASE_URL` — it doesn't know the
  app's dev-server port — so this is a project-side default (the same "adapt to
  cspace" shape as the convex fix: `baseURL` falls back to
  `CSPACE_WORKSPACE_HOST` when present). Optionally, cspace can export the host
  (already done via `CSPACE_WORKSPACE_HOST`) and a project convention doc shows
  the one-line config change.

## 5. Testing strategy

- **Daemon survival (the crux regression test):** spawn the daemon via the
  detached path, exit/kill the parent, then assert the daemon is still alive and
  still answers a `.cspace.test` query. This reproduces the SIGPIPE bug and
  guards it.
- **Version-checked reuse:** stale-version daemon on the port → `ensureRegistryDaemon`
  restarts it.
- **Registry IP refresh:** a sandbox whose IP changed resolves to the new IP,
  never the stale one.
- **Boot-time gate:** provisioning fails loud when in-container resolution
  doesn't work.
- **Env isolation:** build a devcontainer with `.env` carrying `CONVEX_DEPLOYMENT`
  and `.env.cspace` blanking it; assert a **non-login** shell sees it empty.
- **Probes/doctor:** cover the gateway listener + an in-container resolution.

## 6. Sequencing

Ships as **one PR** — this is all related work making cspace's sandbox
environment resolve and isolate reliably. Implemented in this internal order
(commits), highest-leverage first:

1. **Daemon detach + version-checked reuse + boot-time gate + probes** (Part 1a,
   1b). Highest-leverage — fixes the mid-session daemon death that underlies #3
   and #4.
2. **Registry IP refresh + delete the dead hosts-injection plumbing** (Part 1c),
   including the Part 1d clarifications (both browser consumers).
3. **`.env.cspace` convention + docs** (Part 2, Part 3, incl. the e2e `baseURL`
   convention).

## 7. Known limitations / tracked follow-ups

- **MCP/CDP tab isolation.** The shared browser is one Chromium with one CDP
  endpoint; concurrent agents' MCP sessions can, in principle, see/close each
  other's tabs. This is orthogonal to DNS/env and is **out of scope here.**
  Existing empirical finding (maintainer): **Playwright MCP isolates contexts;
  chrome-devtools-mcp did not** at last investigation. Follow-up: confirm the
  current chrome-devtools-mcp behavior and, if still shared, give each agent its
  own browser context (`Target.createBrowserContext` / an `--isolated` mode).
- **launchd-managed daemon** for reboot survival (see Part 1a) — deferred.
