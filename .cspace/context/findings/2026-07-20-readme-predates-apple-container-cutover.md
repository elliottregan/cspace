---
title: README still describes the Docker-era CLI (cspace issue/ssh/list) and needs the reality pass CLAUDE.md got
date: 2026-07-20
kind: finding
status: open
category: observation
tags: docs, readme
---

## Summary
CLAUDE.md was rewritten 2026-07-16 to match the code, but README.md never got the same pass: it still documents commands that no longer exist (`cspace issue`, `cspace ssh`, `cspace list`) from before the Docker → Apple Container cutover. The general-agent branch fixed the specific paragraphs advertising deleted `lib/agents/` playbooks, but the broader staleness predates that branch and remains.

## Details
- Direction: one focused pass diffing README claims against `cspace --help` and CLAUDE.md; the Commands section of CLAUDE.md is the accurate source to mirror.

## Updates
### 2026-07-20T05:30:00Z — @agent — status: open
filed from the general-agent branch's final whole-branch review (surfaced while de-staling the lib/agents references)
