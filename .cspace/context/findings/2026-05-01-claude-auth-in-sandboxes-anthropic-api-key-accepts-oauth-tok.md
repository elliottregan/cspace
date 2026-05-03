---
title: Claude auth in sandboxes: ANTHROPIC_API_KEY accepts OAuth tokens; DNS must be fixed
date: 2026-05-01
kind: finding
status: resolved
category: observation
tags: verification, claude-auth, oauth, dns, p0-extension
related: scripts/spikes/2026-05-01-claude-auth.sh, .cspace/context/findings/2026-05-01-apple-container-default-dns-is-broken-sandboxes-can-t-resolv.md, internal/cli/cmd_prototype_up.go, internal/substrate/applecontainer/adapter.go
---

## Summary
Verified that Claude Code authenticates inside a cspace sandbox using either an `ANTHROPIC_API_KEY` (sk-ant-api-…) or a long-lived OAuth token (sk-ant-oat-…). Two practical gotchas surfaced and were fixed: (1) Claude Code only reads the env var named `ANTHROPIC_API_KEY` — the OAuth token under any other name (e.g. `CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token`) is ignored unless aliased; (2) the DNS finding is a hard prerequisite for auth, since the API call itself can't resolve api.anthropic.com without the `--dns` fix. After both fixes, the spike script PASSes: apiKeySource = ANTHROPIC_API_KEY, real assistant response containing the probe phrase, is_error=false.

## Details
## What broke

When the user dropped their credentials into `.cspace/secrets.env` as:

```
CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-…
```

…and ran `scripts/spikes/2026-05-01-claude-auth.sh`, the spike failed in two distinct ways:

1. **First failure: `apiKeySource: "none"` despite the token being injected into the sandbox env.** Claude Code's CLI does not auto-detect arbitrary env-var names. It reads `ANTHROPIC_API_KEY` only. The CLAUDE_CODE_OAUTH_TOKEN value, even though present in the sandbox env, was ignored.

2. **Second failure: after manually aliasing in-sandbox (`export ANTHROPIC_API_KEY="$CLAUDE_CODE_OAUTH_TOKEN"`), the SDK init showed `apiKeySource: "ANTHROPIC_API_KEY"` but the API call retried with `error: "unknown"`.** Diagnosed via `getent hosts api.anthropic.com` — empty result, no DNS resolution. This is the existing DNS finding (2026-05-01-apple-container-default-dns-is-broken-…). With `--dns 1.1.1.1 --dns 8.8.8.8`, the API call succeeded immediately.

## What was fixed (in this branch)

### 1. `RunSpec.DNS []string` + adapter injection

`internal/substrate/substrate.go` — added `DNS []string` field to `RunSpec`.

`internal/substrate/applecontainer/adapter.go` — `Run` appends `--dns <ns>` for each entry; defaults to `["1.1.1.1", "8.8.8.8"]` when the caller passes none.

This is the same fix the DNS finding recommended; landing it now (rather than waiting for P1 Task 8) because Claude auth — and any other DNS-dependent in-sandbox traffic — is fully blocked without it. Carries the same project-override-via-`.cspace.json` extension into P1.

### 2. `CLAUDE_CODE_OAUTH_TOKEN` → `ANTHROPIC_API_KEY` alias

`internal/cli/cmd_prototype_up.go` — when building the env map, after the secrets file load and the host-shell-env override pass, if `ANTHROPIC_API_KEY` is still empty AND `CLAUDE_CODE_OAUTH_TOKEN` is set, alias the latter onto the former.

Reasoning: `claude setup-token` produces a long-lived OAuth token typically named `CLAUDE_CODE_OAUTH_TOKEN` in user docs and shell exports. Claude Code's runtime accepts these tokens via `ANTHROPIC_API_KEY` (the env var name is unified for both API keys and OAuth tokens). The alias makes either env-var name work without forcing the user to rename.

Precedence stays: `ANTHROPIC_API_KEY` from any source wins over the alias.

## Result (verified)

```json
{
  "raw_lines": 5,
  "apiKeySource_values": ["ANTHROPIC_API_KEY"],
  "auth_ok": true,
  "assistant_text_excerpts": ["CSPACE-AUTH-OK"],
  "probe_seen": true,
  "result_is_error_values": [false],
  "no_errors": true,
  "PASS": true
}
```

## Implications for P1

- The DNS injection is a P1 Task 8 deliverable per the existing plan; this commit lands the substrate-side change early. P1 Task 8 still needs to add the `Sandbox.DNS []string` config field and wire it through `cspace2-up` for project overrides.
- The token alias is a P0-extension behavior carried forward; it should remain in P1's `cspace2-up`. Document the supported credential variable names (ANTHROPIC_API_KEY first, CLAUDE_CODE_OAUTH_TOKEN second) in the secrets section of CLAUDE.md.
- Future spike scripts can now rely on Claude auth working as long as the user has either credential in `.cspace/secrets.env`. Real-Claude tests (multi-turn with conversation memory, tool-use, etc.) become testable.

## POC concessions

- Token alias is one-directional (CLAUDE_CODE_OAUTH_TOKEN → ANTHROPIC_API_KEY). Reverse alias not implemented; not needed since Claude Code reads only the destination var.
- DNS resolvers are hardcoded defaults (1.1.1.1 / 8.8.8.8). The project override path (`.cspace.json`) is P1 work.
- No telemetry-redaction safeguards yet for the OAuth token in process env; the vminitd env-leak finding still applies. Same risk profile as ANTHROPIC_API_KEY.

Status: resolved. Auth path is unblocked for all subsequent spikes.

## Updates
### 2026-05-01T04:44:43Z — @agent — status: resolved
filed
