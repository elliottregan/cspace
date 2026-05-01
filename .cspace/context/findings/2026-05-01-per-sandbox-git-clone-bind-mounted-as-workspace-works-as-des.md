---
title: Per-sandbox git clone bind-mounted as /workspace works as designed
date: 2026-05-01
kind: finding
status: resolved
category: observation
tags: verification, workspace, git, p0-extension, p1-design
related: scripts/spikes/2026-05-01-workspace-clone.sh, docs/superpowers/specs/2026-04-30-sandbox-architecture-design.md, docs/superpowers/plans/2026-05-01-phase-1-canonical-cspace2.md
---

## Summary
Open question raised during P1 review: how does cspace map a host project into a sandbox in a way that (a) makes /workspace look like a normal main-worktree to the agent (so lefthook/husky/pnpm don't break), (b) keeps each sandbox's filesystem isolated from siblings and from the host's main worktree, and (c) lets the host see the agent's commits without a network round-trip? Tested via `scripts/spikes/2026-05-01-workspace-clone.sh`: per-sandbox `git clone <project> ~/.cspace/clones/<project>/<sandbox>` → bind-mount that clone → /workspace. Five properties asserted, all PASS. Locking this design in for P1.

## Details
## Test method

`scripts/spikes/2026-05-01-workspace-clone.sh` exercises the full workflow:

1. Host: `git clone <project-root> ~/.cspace/clones/<project>/<sandbox>` and `git checkout -b cspace/<sandbox>`.
2. Bring up the sandbox via `cspace prototype-up <sandbox> --workspace <clone-path>` (the `--workspace` flag is a temporary P0-extension; P1's `cspace2-up` will own clone provisioning end-to-end).
3. Verify the sandbox sees a normal repo at `/workspace`.
4. Inside the sandbox, write a probe file and `git commit` it via `container exec`.
5. Verify the host sees the file content and the commit at the clone path with no sync step.
6. From the project main repo, `git fetch <clone-path> cspace/<sandbox>` and verify the branch + commit arrived without going through any network.
7. Verify the project main worktree is unchanged.

## Result (all checks PASS)

```
/workspace inside sandbox:
  is-inside-work-tree: true
  is-inside-git-dir:   false
  git-dir:             .git           # directory, not a worktree pointer file
  show-toplevel:       /workspace     # the main worktree, no subtree weirdness
  current branch:      cspace/wstest

After agent commit:
  - probe file visible on host at clone path with expected content
  - commit visible via `git log` at the clone path
  - project main repo fetches the branch successfully (no network round-trip)
  - project main worktree NOT touched (isolation verified)
```

## What this proves

- **Agent UX is "normal git repo".** `.git/` is a regular directory, the branch is the active branch of the only worktree, `git rev-parse --show-toplevel` returns `/workspace`. Tooling like lefthook, husky, pnpm, and anything that touches `.git/` paths sees a standard layout.
- **Bind-mount semantics work as expected.** Files written inside the sandbox are immediately readable from the host at `~/.cspace/clones/<project>/<sandbox>/`. This includes all `.git/` updates (refs, objects, index).
- **Isolation is real.** The project's main worktree at `<project-root>` is not touched by sandbox activity. Sibling sandboxes get separate clones at separate paths and cannot interfere with each other.
- **Host integration is one local fetch away.** `cd <project-root> && git fetch <clone-path> cspace/<sandbox>` brings the agent's branch into the project's main `.git/` with no GitHub round-trip. Suitable for fast inner-loop review.

## Locked design (for P1)

- **Clone path:** `~/.cspace/clones/<project>/<sandbox>` (host home; outside the project tree to avoid polluting it; one entry per sandbox).
- **Branch naming:** `cspace/<sandbox>` (visually obvious in `git branch` output).
- **Base branch:** default = current HEAD of the host project at `cspace2-up` time. Override via `--base <branch>` in P1's command.
- **Mount:** bind `<clone-path>` → `/workspace` in the sandbox; sandbox cwd is `/workspace`.
- **Cleanup:** `cspace2-down` keeps the clone on disk by default (don't lose work). A `--remove-clone` flag and a separate `cspace2 worktree prune`-style command land in P1 housekeeping.
- **Concurrent-sandbox safety:** each sandbox's clone has its own `.git/`. No shared lock files, no shared object store (until --shared optimization is layered on later).

## P1 implementation notes

- `cspace2-up` should absorb clone provisioning (currently in the spike script). Pseudocode: `if !exists(clonePath) { git clone projectRoot clonePath; cd clonePath && git checkout -b cspace/<name> }`.
- The `--workspace` flag added to `prototype-up` for this spike (`internal/cli/cmd_prototype_up.go`) carries forward as the underlying mechanism inside `cspace2-up`. Replace explicit flag with auto-derived clone path.
- Optimization for big-history projects: switch `git clone` → `git clone --shared` to share object store with the project's main repo. Caveat: don't `git gc` the main repo while sandboxes hold references. Acceptable for cspace's typical use.
- Non-git projects: fall back to bind-mounting `<project-root>` directly, with a warning. Branch / commit guarantees don't apply.

## POC concessions in the spike script

- Hardcoded sandbox name `wstest`; concurrent runs would collide.
- Synchronous `git clone` on the host before sandbox boot (acceptable; subsecond for typical projects).
- Commits inside the sandbox use `git -c user.email/user.name` to avoid depending on container-side user config — not a real-Claude commit. The Claude-driven version is a follow-up spike once auth is wired (`scripts/spikes/2026-05-01-claude-auth.sh` covers auth verification independently).
- `KEEP_CLONE` env var is the only knob; production cspace2-down will have a real `--remove-clone` flag.

Status: resolved. Design is locked for P1.

## Updates
### 2026-05-01T04:29:17Z — @agent — status: resolved
filed
