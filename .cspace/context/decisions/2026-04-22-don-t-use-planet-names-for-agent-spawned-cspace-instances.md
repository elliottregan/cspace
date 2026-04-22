---
title: Don't use planet names for agent-spawned cspace instances
date: 2026-04-22
kind: decision
---

## Context
cspace reserves planet names (mercury, venus, earth, …) for the human-facing TUI which auto-assigns them via port-range heuristics in internal/ports. Agents manually spawning sub-instances with planet names collide with what the TUI would otherwise pick, and make human vs agent traffic indistinguishable in docker ps.

## Alternatives
Allow but warn (rejected: too easy to ignore); reserve the names in config (rejected: changes a live user-facing behavior).

## Decision
Agents spawning cspace instances use descriptive or numbered names (issue-<n>, short task labels like review-fixes, search-bootstrap, or cs-agent-<n>). Planet names are reserved for TUI-auto-assigned instances.

## Consequences
Predictable separation of human vs agent instances in cspace list / docker ps; fewer port collisions; agents must pick names explicitly.
