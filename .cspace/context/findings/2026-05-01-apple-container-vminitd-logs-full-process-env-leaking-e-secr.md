---
title: Apple Container vminitd logs full process env, leaking -e secrets
date: 2026-05-01
kind: finding
status: open
category: observation
tags: security, apple-container, secrets, leak, p1-blocker
related: https://github.com/apple/container/discussions/1153, docs/superpowers/specs/2026-04-30-sandbox-architecture-design.md, docs/superpowers/reports/2026-05-01-phase-0-prototype-report.md
---

## Summary
Apple Container's per-VM init process (`vminitd`) logs the full environment of every spawned process. Secrets passed via `-e` or `--env-file` to `container run` therefore appear in plaintext in `container logs --boot <name>`. This is a known oversight upstream (apple/container discussion #1153, acknowledged by maintainers), still present in v0.12.3. cspace's Phase 0 secret delivery transits `-e` flags, so any value loaded from `.cspace/secrets.env` — including `ANTHROPIC_API_KEY` — is readable by anyone with `container logs` access on the host. The leak is independent of where the secret is stored (file, Keychain, 1Password); it's a delivery-mechanism problem.

## Details
## Reproduction

```bash
container run -d --name leaktest -e SECRET_VALUE=visible-in-logs alpine sleep 60
container logs --boot leaktest 2>&1 | grep SECRET_VALUE
# vminitd boot log includes the full process env, SECRET_VALUE=visible-in-logs is visible
container stop leaktest && container rm leaktest
```

## Impact on cspace P0

- `ANTHROPIC_API_KEY` from `~/.cspace/secrets.env` or `<project>/.cspace/secrets.env` is delivered via `RunSpec.Env` → `container run -e KEY=value` (see `internal/cli/cmd_prototype_up.go` and `internal/substrate/applecontainer/adapter.go:Run`).
- Any user with read access to the host's apicontainer runtime state can read these secrets via `container logs --boot <sandbox-name>`.
- On a single-user laptop this is mostly an "anyone-with-shell-access" risk. On shared hosts or CI runners it's a real exposure.
- Documented as a known caveat in `CLAUDE.md` ("Agent secrets" section) and the Phase 0 prototype report.

## Mitigations (in priority order, all P1)

1. **Stop using `-e` for secret values.** Options to evaluate during P1 design:
   - Tmpfs mount: write secrets to a tmpfs file inside the sandbox at boot; the supervisor reads them and removes the file. Doesn't transit any logged path. Preferred default if Apple Container exposes a tmpfs-style mount or `--tmpfs` flag.
   - Stdin to supervisor: pipe a JSON envelope of secrets to the supervisor over its stdin at start. Supervisor consumes once, then the pipe closes. Doesn't appear in any process arg or env table.
   - Unix socket pickup: supervisor connects to a per-sandbox socket on the host gateway IP, fetches its secret bundle, then never speaks to it again. More moving pieces but doesn't depend on substrate-specific delivery features.

2. **Add `keychain:<service-name>` value-prefix support** in `internal/secrets/secrets.go` so the file format becomes an *index* and values are resolved at load time via `security find-generic-password -s <service> -w` (macOS) or `secret-tool` (Linux GNOME Keyring) or similar. Keeps secrets off disk; orthogonal to (1) but a complementary defense.

3. **Document operationally** that `container logs --boot` must not be shared/exported off-host until upstream fixes the leak.

## Tracking

Watch apple/container discussion #1153 for a fix. Until it lands, P1 design must include item (1) above before any sandbox runs in a context where `container logs` is reachable by anyone other than the sandbox owner.

## Related code

- Delivery point: `internal/cli/cmd_prototype_up.go` (env map → `RunSpec.Env`)
- Adapter: `internal/substrate/applecontainer/adapter.go:Run` (`-e KEY=value` flag construction)
- Loader: `internal/secrets/secrets.go` (file → map; this is where keychain: prefix would be resolved)
- Spec section to update in P1: "Per-sandbox internals → Secrets delivery" in `docs/superpowers/specs/2026-04-30-sandbox-architecture-design.md`

## Updates
### 2026-05-01T02:26:24Z — @agent — status: open
filed
