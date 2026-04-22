---
title: Don't compare counts when limits are in play — compare identities or timestamps
date: 2026-04-22
kind: decision
---

## Context
CommitsStaleness compared rev-list --count to len(indexed). With commits.limit: 500 and a 1000-commit repo, the difference is always 500, so the staleness signal was permanently stuck at "500 new commits".

## Alternatives
Clamp the rev-list count to the limit (rejected: doesn't answer "is HEAD newer than last indexed?"); treat indexed == limit as special (rejected: fragile).

## Decision
When detecting "X is newer than Y" across a bounded window, compare the identity of the newest element (timestamp, hash, sequence id) — never raw size. Applies to any staleness/change-detection check going forward.

## Consequences
Change-detection logic needs to surface timestamps/hashes in payloads; slightly richer qdrant payload schema.
