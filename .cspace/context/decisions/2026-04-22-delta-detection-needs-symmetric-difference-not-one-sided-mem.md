---
title: Delta detection needs symmetric difference, not one-sided membership
date: 2026-04-22
kind: decision
---

## Context
CodeStaleness counted tracked files whose hash was absent from the index — but never counted indexed entries whose path/hash was no longer tracked. Deleted-but-still-indexed ghosts made results stale in a way the signal couldn't surface.

## Alternatives
One-sided check with documented known-unknowns (rejected: erodes trust).

## Decision
Any "is A in sync with B" check computes A - B and B - A and reports both sides. One-sided checks are a code smell for sync logic.

## Consequences
Slightly more code per check; better signal fidelity.
