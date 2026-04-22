---
title: Secondary work injected into query paths needs a latency budget
date: 2026-04-22
kind: decision
---

## Context
mcpAppendStalenessWarning added 100-500ms of git I/O + SHA256 to every MCP search call. Agents doing 5+ queries mid-turn paid seconds of invisible overhead.

## Alternatives
Do the check async (rejected: result not available when query returns); move the check entirely off-path (rejected: agents forget to call it); cache the result (accepted).

## Decision
Any synchronous check bolted onto a user-facing command must either (a) be cached with a TTL appropriate to the volatility of the input, (b) be timeboxed to <50ms with graceful fall-through, or (c) be opt-in via a flag. Default to caching when the input is slow-changing.

## Consequences
First query in a session is slightly slower; subsequent queries are fast; staleness signals may lag by up to the TTL.
