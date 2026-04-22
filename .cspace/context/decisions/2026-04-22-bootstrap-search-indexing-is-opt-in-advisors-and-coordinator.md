---
title: Bootstrap search indexing is opt-in; advisors and coordinators get it by default
date: 2026-04-22
kind: decision
---

## Context
Auto-indexing at container boot blocks provisioning by minutes and wastes resources in the common case where search isn't queried that session. Advisors and coordinators are search-heavy and session-continuous, so the cost amortizes. Everyone else pays only when they explicitly opt in.

## Alternatives
(a) always bootstrap in background (current) — rejected: still CPU-intensive, races with agent startup; (b) never bootstrap at boot — rejected: advisors would hit cold indexes every consultation; (c) role-aware default with opt-in flag — chosen.

## Decision
Search indexing at provision time is opt-in via BootstrapSearch on provision.Params. Advisors (advisor.Launch) and coordinators (runCoordinateWithArgs) set it true. Interactive cspace up defaults to false; users pass --index to enable. TUI flows skip it.

## Consequences
Interactive cspace up returns faster; autonomous agents do not pay an indexing tax they may not need; advisors/coordinators retain the warm-start behavior they depend on.
