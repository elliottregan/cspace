---
title: host-to-container iteration goes through git remote because host fs is not bind-mounted
date: 2026-05-05
kind: finding
status: acknowledged
category: observation
tags: devcontainer-adoption, host-iteration, ergonomics, v1.0
related: https://github.com/elliottregan/cspace/issues/73, https://github.com/elliottregan/cspace/issues/69
---

## Summary
Live collaboration between a human on the host and a Claude agent inside a cspace sandbox is possible but asymmetric. Container-to-host changes flow naturally through the workspace bind mount (host can directly read `~/.cspace/clones/<project>/<sandbox>/`), but host-to-container changes require a round-trip through a reachable git remote (typically `origin` on GitHub), or an out-of-band channel like dropping files into the bind-mounted `~/.cspace/sessions/<project>/<sandbox>/` (visible inside the sandbox as `/sessions/`). The intended fast path — the `host` git remote that `provisionClone` configures — is dead on arrival inside the sandbox because its URL points at a host filesystem path (`/Users/<user>/Projects/<project>`) that isn't mounted into the microVM.

## Details
## Repro

Inside any cspace v1 sandbox with a workspace clone:

```
$ git remote -v
host    /Users/elliott/Projects/resume-redux (fetch)
host    /Users/elliott/Projects/resume-redux (push)
origin  https://github.com/elliottregan/resume-redux.git ...

$ git pull host devcontainer-adoption
fatal: '/Users/elliott/Projects/resume-redux' does not appear to be a git repository
```

`provisionClone` configures the `host` remote with the URL it cloned from (the host filesystem path). That URL is unreachable from inside the sandbox — Apple Container microVMs don't see the host's filesystem unless it's explicitly bind-mounted, and cspace doesn't mount it.

## Surfaced during

End-to-end shakedown of resume-redux on cspace v1's `devcontainer-adoption` branch (Issue #69 canary). I made two host-side fixes (`MODE=local` removal in devcontainer.json + README docs; `e2e/global-setup.ts` regex bug), committed them, told the agent inside the sandbox to `git pull host devcontainer-adoption` and retry the e2e suite. Pull failed; agent correctly diagnosed the missing mount and stopped rather than retrying blindly.

## Asymmetry of the data flow

- **Container → host: works.** Agent writes `/workspace`, which is `~/.cspace/clones/<project>/<sandbox>/` on host. Host can `cat`, `git fetch`, or open in an editor directly. Bind-mount makes this transparent.
- **Host → container: broken via the intended fast path.** The `host` remote in the workspace clone points at the host filesystem path that isn't bind-mounted. Workarounds: (a) commit + `git push origin && git pull origin`, (b) `git format-patch` and drop the patch into `~/.cspace/sessions/<project>/<sandbox>/` (visible in-sandbox as `/sessions/`), then `git am`.

## Why it matters in practice

Iteration loop with an agent typically goes: human edits a file on host → wants the agent to pick it up and retry. The intended fast path was `git pull host` — instant, no network, no WIP commits to clutter the branch. With it broken, every iteration becomes a push + pull through GitHub, which is slow and pollutes git history.

The workaround via `/sessions` is fine for one-off patches but doesn't scale to "fix three things and have the agent see them" — and it's discoverable only by people who already know the bind-mount layout.

## Mitigations

1. **Add a host-project bind mount during provisionClone** (preferred fix, scope tracked in cspace#73). Read-only mount of host project root at e.g. `/cspace/host-project`, plus `git remote set-url host /cspace/host-project`. Worktree complication: if the host is itself a git worktree, also mount `git rev-parse --git-common-dir` to make the worktree's `.git` file resolvable. ~30-60 min implementation.
2. **Document the existing /sessions workaround** in cspace docs so users have a discoverable path until #73 lands.
3. **Defensive logging on `cspace up`**: detect that `host` URL won't resolve inside the sandbox and warn at provision time, so users aren't surprised mid-iteration.

## Severity

Observation, not bug — `host` remote being non-functional doesn't break any documented cspace workflow (origin still works). It's a UX paper-cut that becomes visible in collaborative-iteration scenarios. Frequency: low (most agent runs are autonomous; only matters when a human is actively editing on the host while an agent is mid-task in a sandbox).

## Workaround template

For agents instructed to "pull the host fix and retry":

```
# host side
cd ~/Projects/<project>
git format-patch -N HEAD --output-directory=~/.cspace/sessions/<project>/<sandbox>/

# in-sandbox
cd /workspace
git am /sessions/*.patch
rm /sessions/*.patch
```

## Updates
### 2026-05-05T15:47:50Z — @agent — status: acknowledged
filed
