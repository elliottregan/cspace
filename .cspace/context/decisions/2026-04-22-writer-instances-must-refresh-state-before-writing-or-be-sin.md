---
title: Writer instances must refresh state before writing OR be single-use
date: 2026-04-22
kind: decision
---

## Context
PR #61's runSearchInit held a long-lived status.Writer whose in-memory snapshot was wiped by concurrent inner writers; the next mutation clobbered everything. Single-use writers (created at call-site, flushed once, discarded) or read-modify-write semantics avoid the footgun.

## Alternatives
A singleton writer with inner locks (rejected: locks don't solve cross-process or re-entrant reads); read-before-every-write (rejected: slower, harder to reason about).

## Decision
For status/context-like writers where multiple callers in a process may mutate the same file, callers construct the writer at the narrowest useful scope and discard after flushing. Long-lived writers must re-read on each mutation or be explicitly documented as single-writer-per-process.

## Consequences
Fewer surprising clobbers; some redundant reads.
