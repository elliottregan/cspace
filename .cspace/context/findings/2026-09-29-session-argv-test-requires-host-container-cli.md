---
title: Targeted session argv test requires the host container executable
date: 2026-09-29
kind: finding
status: resolved
category: bug
tags: control-plane, sessions, tests, linux, ci
---

## Summary
The targeted session argv test passed on a Mac with Apple Container installed
but failed on Linux CI because AttachArgv resolves the container executable.

## Details
TestExistingAttachArgvPinsTheSessionInsideTmux exercised a pure command-building
contract without providing an executable lookup fixture. CI run 36620593619
failed at that lookup before checking the session identity guards.

## Updates
### 2026-09-29 — status: resolved
The test installs a temporary executable and limits PATH to its directory,
then verifies AttachArgv selected that exact executable. This removes the
host dependency while retaining the targeted-reconnect assertions.
