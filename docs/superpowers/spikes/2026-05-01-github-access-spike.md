# GitHub Access from Inside Sandbox — Spike — 2026-05-01

## Goal

Verify that an agent inside a cspace2 sandbox (apple `container` substrate, Phase 0
prototype image) can:

1. Use `gh` CLI to view repos, list issues, and call authenticated endpoints, with
   `GH_TOKEN` loaded from `.cspace/secrets.env`.
2. Use `git` to clone over HTTPS and probe push auth via `git ls-remote`.
3. Establish that the same flow will work for the GitHub MCP server (which reads
   `GITHUB_TOKEN`).

## Setup

- Image: `cspace-prototype:latest`, rebuilt from `lib/templates/Dockerfile.prototype`
  with the gh apt repo + `gh` package added (the same Dockerfile snippet specified
  by the P1 plan, Task 4).
- Token source: `gh auth token` extracted from the host's gh keyring; written to
  `.cspace/secrets.env` as both `GH_TOKEN` and `GITHUB_TOKEN`.
  `.cspace/secrets.env` is gitignored (`.gitignore` line 19) so the token never
  reaches the index.
- Sandbox: `cspace prototype-up gh1` -> container `cspace-cspace-gh1` at
  `192.168.64.40`.
- gh on host: `gh version 2.92.0 (2026-04-28)`.
- gh in sandbox: `gh version 2.92.0 (2026-04-28)` (same Debian apt build).

## Results

### gh CLI (after DNS workaround, see Caveats)

```
$ gh --version
gh version 2.92.0 (2026-04-28)
https://github.com/cli/cli/releases/tag/v2.92.0

$ gh auth status
github.com
  ✓ Logged in to github.com account elliottregan (GH_TOKEN)
  - Active account: true
  - Git operations protocol: https
  - Token: gho_************************************
  - Token scopes: 'gist', 'read:org', 'repo', 'workflow'

$ gh repo view elliottregan/cspace --json name,description,defaultBranchRef
{"defaultBranchRef":{"name":"main"},"description":"Portable CLI for managing isolated Claude Code devcontainer instances","name":"cspace"}

$ gh issue list --repo elliottregan/cspace --limit 3
64  OPEN  Architecture: sandbox-per-top-level-session …  enhancement, question  2026-04-24T03:35:28Z
59  OPEN  Local-first task/inbox system …                enhancement, question  2026-04-22T01:13:29Z
56  OPEN  Cross-corpus retrieval for "why did we do X"   enhancement            2026-04-21T16:59:08Z

$ gh api user --jq .login
elliottregan
```

### git clone over HTTPS

```
$ git clone https://github.com/elliottregan/cspace.git clone-test
Cloning into 'clone-test'...
$ git -C clone-test log --oneline -3
24e2b2a Remove Go cspace-search subsystem (#65)
7699862 Add SmolVM feasibility evaluation
558b8be Compact statusline port links by default
```

### git ls-remote (auth probe — would push work?)

```
$ git -C /tmp/clone-test ls-remote
From https://github.com/elliottregan/cspace.git
24e2b2a6d512df7539aa51f3c42e6dbeac583161  HEAD
03e39c89355487bcb3daa379afb595b0d09b4f31  refs/heads/claude/document-agent-sdk-usage-8s1aA
…
```

### gh credential helper integration

`gh auth setup-git` writes the helper to `/root/.gitconfig`:

```
[credential "https://github.com"]
    helper =
    helper = !/usr/bin/gh auth git-credential
```

`gh auth git-credential get` returns a working `x-access-token` token pair
(verified with `echo "url=https://github.com" | gh auth git-credential get`). For
this spike `git ls-remote` and `gh repo clone` both succeeded.

## Verdict

- gh CLI inside sandbox: **PASS** (after DNS workaround)
- git HTTPS auth via gh credential helper: **PASS** (after DNS workaround)
- GitHub MCP server (claude-code config): **NOT TESTED** — `GITHUB_TOKEN` is in
  the sandbox env, so the MCP server should pick it up; the actual claude
  registration for the MCP server is out of scope for this spike.

## Caveats / gotchas

### DNS is broken in the prototype sandbox (out of the box)

This is the only blocker, and it is **not** a Phase 1 / gh problem — it is a
substrate networking problem already half-noted in
`spikes/2026-04-30-apple-container-spike.md`. Reproduced here in detail.

- `/etc/resolv.conf` in a freshly-launched prototype sandbox contains exactly
  `nameserver 192.168.64.1` (the host gateway). Apple's `container` runtime sets
  this and intends the gateway to forward DNS, but on this machine the gateway
  does not answer port 53 for **anything**:

  ```
  $ getent hosts api.github.com         # via 192.168.64.1
  (empty — no answer)
  $ nslookup api.github.com 192.168.64.1
  ;; communications error to 192.168.64.1#53: timed out
  ```

- HTTPS to GitHub itself works fine (verified with
  `curl --resolve api.github.com:443:140.82.121.6 https://api.github.com/user`
  returning HTTP 200 with the bearer token), so the firewall and TLS path are
  healthy. **Only DNS resolution fails.**
- gh's error message in this state is misleading — it says
  *"The token in GH_TOKEN is invalid"*, when in fact the token is fine and the
  HTTP request never reaches GitHub. We confirmed token validity by direct
  curl.
- Workaround used in the spike: overwrote `/etc/resolv.conf` inside the
  sandbox with `nameserver 1.1.1.1` + `nameserver 8.8.8.8`. After that, every
  probe (gh auth status, gh repo view, gh issue list, gh api user, git clone,
  git ls-remote, gh repo clone) passed. This is **not** a viable production
  fix — it bypasses any future per-project egress proxying — but it cleanly
  isolates the failure to DNS.

### Token-name duplication

The GitHub ecosystem is split: gh prefers `GH_TOKEN`, the official GitHub MCP
server (and most CI tooling) prefers `GITHUB_TOKEN`. Setting both costs nothing
and is what the spike instructions already prescribed. Worth keeping that
guidance in the P1 secrets doc.

### gh is ~50MB on top of the prototype image

The apt install pulls gh + dependencies. Final image still well under 1GB, so
no concern, but worth noting if image-size budgets tighten.

## Implications for P1

- **Task 4 (add gh to the canonical Dockerfile) is effectively done.** The
  Dockerfile snippet from the P1 plan works as-written; this spike kept the
  change on `phase-0-prototype` so it's already merged into the image P1 will
  inherit.
- **A new prerequisite surfaces: fix DNS in the sandbox before P1 ships.**
  Options, roughly in order of preference:
  1. Bake `nameserver 1.1.1.1` and `nameserver 8.8.8.8` into the image
     (`COPY` or `RUN echo` to `/etc/resolv.conf` after entrypoint setup).
     Simple but ignores per-project DNS policy.
  2. Have `cspace prototype-up` / `cspace2-up` write `/etc/resolv.conf` via
     `container exec` after start, populated from a configurable list. Adds
     a host->container coupling; not great.
  3. Investigate why `192.168.64.1:53` is dead on this host. The apple-
     container spike treats sibling DNS via `container system dns create` as
     "admin required, fragile", but the same gateway is supposed to forward
     external DNS unconditionally. May be a host-OS firewall issue specific
     to this machine; worth checking on a clean macOS install.
  4. Run a tiny in-cluster DNS forwarder (e.g. dnsmasq in a sidecar). Heavy.

  Recommendation: pick (1) for P1's first cut and file an issue to
  investigate (3). Without it, every gh / npm / git / curl operation hits the
  same wall and produces misleading auth errors.
- **GH_TOKEN flows cleanly via `.cspace/secrets.env` -> `-e` flag -> in-sandbox
  env**, with no code changes needed. Same security caveat (vminitd env-var
  leak) noted in the apple-container spike still applies and is explicitly
  deferred per the P1 plan.
- **gh credential helper "just works"** for git over HTTPS once gh is
  authenticated. No need to provision a separate git credential file.
- The misleading "token is invalid" error from gh on DNS failure is worth a
  one-line note in the P1 troubleshooting doc — it cost ~5 minutes of this
  spike.
