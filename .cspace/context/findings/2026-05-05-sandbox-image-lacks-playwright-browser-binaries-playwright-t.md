---
title: Sandbox image lacks Playwright browser binaries — playwright tests need install at runtime
date: 2026-05-05
kind: finding
status: resolved
category: refactor
tags: sandbox-image, papercut, post-1.0
---

## Summary
cspace's image installs the Playwright NPM package globally (for the playwright-mcp tool the agent uses) but doesn't pull the actual browser binaries (~120 MB for Chromium headless shell). Projects running `playwright test` from inside a sandbox need to either run `playwright install --with-deps chromium` first (~30 s download per fresh sandbox) or rely on the cspace --browser sidecar (different protocol — CDP, not Playwright Server). resume-redux's e2e suite defaults to launching a local browser; without the binaries it fails with "Executable doesn't exist at /home/dev/.cache/ms-playwright/...".

## Details


## Updates
### 2026-05-05T00:43:30Z — @agent — status: open
filed

### 2026-05-05T03:50:13Z — @agent — status: resolved
Resolved by 3b8d1c4. Sandbox image now bakes Chromium + headless_shell + ffmpeg under /opt/ms-playwright via PLAYWRIGHT_BROWSERS_PATH. Verified inside the rebuilt image:

```
$ ls /opt/ms-playwright
chromium-1217  chromium_headless_shell-1217  ffmpeg-1011  ...
```

`playwright install --with-deps chromium` ran during build, OS deps (libnss3, libxkbcommon0, …) are now pre-installed. ~120 MB layer added; one-time cost per host (or per ghcr.io pull when image distribution lands). Projects running `playwright test` from inside a sandbox launch the bundled browser instantly with no per-sandbox download. Verified earlier against resume-redux: 4/6 e2e specs passed once the browsers were available.
