---
title: supervisor could drop Bun for the Node already in the image
date: 2026-08-28
kind: finding
status: acknowledged
category: refactor
tags: supervisor, build, toolchain, image, design
---

## Summary
The supervisor is written in TypeScript and compiled by Bun into a single Linux
binary, from a dedicated `oven/bun:1.3-debian` builder stage. The image it runs
in already ships Node. Elliott no longer works in Bun environments and is open
to compiling with Node if it simplifies things — parked 2026-08-28 as a
subsystem change deserving its own design pass, not a patch.

## Details
Measured surface, not estimated:

- **Production code uses exactly one Bun API**: `Bun.serve` in `main.ts:126`.
  Everything else is plain TypeScript. `routes.ts` was deliberately written as
  pure functions "so they can be tested without spinning up Bun.serve", so the
  seam a port would need already exists.
- **Six test files import `bun:test`** (45 tests). `describe` / `test` /
  `beforeEach` / `afterEach` map onto `node:test`, but `expect(...)` does not —
  it would become `node:assert`, touching every test.
- **Build is `bun build --compile --target=bun-linux-arm64`**, which bundles
  dependencies into the binary.

What dropping Bun would buy: the builder stage disappears, along with the
glibc-alignment constraint the Dockerfile warns about at length (a musl-linked
supervisor fails at exec time in the glibc runtime stage), and contributors stop
needing a second toolchain. It also removes a skew already visible — the builder
pins Bun 1.3 while the runtime's `curl bun.sh/install` now lands 1.4.

What it costs: a packaging story. Bun bundles deps; Node needs either a bundler
(esbuild) or `node_modules` shipped in the image. Node 26 can run TypeScript
directly via type stripping, which could mean no build step at all — but then
`@anthropic-ai/claude-agent-sdk` has to be installed into the image, and type
stripping does not support every TS feature.

Separately: the image also installs Bun at runtime for agents' own use
(`curl bun.sh/install`, ~38 MB). That is a distinct question from the build
toolchain — other projects may want it even if cspace stops compiling with it.

## Updates
### 2026-08-28 — status: acknowledged
Parked at Elliott's request. Related: the runtime-versions finding from the same
day, which is the other half of "what should the image provide."
