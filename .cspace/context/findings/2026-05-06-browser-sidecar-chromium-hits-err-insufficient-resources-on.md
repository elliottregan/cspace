---
title: browser sidecar Chromium hits ERR_INSUFFICIENT_RESOURCES on first paint
date: 2026-05-06
kind: finding
status: resolved
category: bug
tags: browser, sidecar, playwright-mcp, chrome-devtools-mcp, resources
---

## Summary
When MCP-driven Chromium in the cspace browser sidecar loads a moderately complex page (resume-redux's Nuxt preview), 6-9 subresources fail with `net::ERR_INSUFFICIENT_RESOURCES` even though the same URLs return 200 from curl inside the same network. On a cache-busting reload it gets worse: page renders completely unstyled because the CSS chunks fail to fetch. Reproduces across both `mcp__playwright__*` and `mcp__chrome-devtools__*` since they share the same sidecar Chromium.

## Details


## Updates
### 2026-05-06T04:48:51Z — @agent — status: open
filed

### 2026-05-06T05:12:15Z — @agent — status: resolved
Resolved by `--disable-dev-shm-usage` on the sidecar Chromium (commit fe18a7f, internal/cli/browser.go).

End-to-end verification on rc12-probe (resume-redux) using the same `mcp__chrome-devtools__navigate_page(reload, ignoreCache: true)` sequence that originally produced 9 errors and an unstyled page:

| metric | before | after |
|---|---|---|
| `ERR_INSUFFICIENT_RESOURCES` count | 9 | **0** |
| Page render | unstyled blue links | fully styled (Tailwind, dot grid, serif fonts, brown CTA) |

Chrome args confirmed in the running sidecar: `chrome --headless=new --no-sandbox --disable-gpu --disable-dev-shm-usage --remote-debugging-port=9223 about:blank`. /dev/shm starts empty (0/64 MiB used) and stays empty under load — Chrome routes URLLoader buffers to /tmp instead. (cs-finding:2026-05-06-browser-sidecar-chromium-hits-err-insufficient-resources-on)
