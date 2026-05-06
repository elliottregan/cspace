---
title: Sandbox image ships pnpm 9; many projects pin pnpm 10
date: 2026-05-05
kind: finding
status: resolved
category: refactor
tags: sandbox-image, papercut, rc-blocker
---

## Summary
cspace's sandbox image installs pnpm 9.x globally via `npm install -g pnpm@9` in the Dockerfile. Modern projects (resume-redux pinned pnpm 10.30.1, similar for many JS repos) declare `packageManager: pnpm@10.x` in package.json and use pnpm-10-only syntax in pnpm-workspace.yaml (e.g. `overrides:` at the top level). Bare `pnpm install` then crashes with "packages field missing or empty"; users have to manually run `corepack pnpm install` (which respects packageManager) for every project script. Worse: `pnpm run dev` invokes the system pnpm 9 inside the script, defeating the corepack workaround.

## Details


## Updates
### 2026-05-05T00:42:41Z — @agent — status: open
filed

### 2026-05-05T03:50:03Z — @agent — status: resolved
Resolved by 3b8d1c4. Sandbox image now installs pnpm 10 globally (current major; verified inside the rebuilt image as `10.33.3`) and enables corepack, so projects pinning specific pnpm versions via package.json's `packageManager:` field get them transparently. Verified end-to-end: a fresh sandbox runs `pnpm install` cleanly against resume-redux's pnpm-10-pinned package.json without the corepack workaround.
