# Playwright Under Apple Container — Spike — 2026-05-01

## Goal

Prove Apple Container can run Chromium via Playwright before designing P2's
per-implementer browser story. Substrate verification only — not a cspace
integration.

## Setup

- Image: `mcr.microsoft.com/playwright:v1.58.0-noble`
- Image size on disk (per `container image inspect`):
  - linux/arm64 manifest: 909 MB compressed → ~2.46 GB unpacked
  - linux/amd64 manifest: 903 MB compressed → ~2.22 GB unpacked
  - Apple Container 0.12.3 unpacked **both** arch variants on a single
    `image pull` even though the host is arm64; total disk hit was ~4.7 GB.
    There is no `--platform` flag on `container image pull` in 0.12.3, so
    selecting one arch at pull time isn't possible — only at `run` time.
- Pull wall time: **1m 33s** (unpacking dominated; the multi-arch unpack
  ran sequentially, ~10s per arch for the unpack step).
- Run mode: one-shot `container run --rm` with `/tmp/cspace-playwright-spike`
  bind-mounted at `/work`.
- Apple Container version: 0.12.3.
- Host: Darwin 25.4.0 (macOS 15) on Apple Silicon.

### Image contents (relevant pieces)

- Ubuntu 24.04 (Noble) base.
- Node.js v24.13.0 at `/usr/bin/node` (apt-installed from nodesource).
- `PLAYWRIGHT_BROWSERS_PATH=/ms-playwright` is preset in the image's env.
- `/ms-playwright/` contains pre-installed binaries for `chromium-1208`,
  `chromium_headless_shell-1208`, `ffmpeg-1011`, `firefox-1509`,
  `webkit-2248`.
- The Playwright npm package itself is **not** installed — Microsoft's
  Dockerfile uses `playwright-core` to install browsers via a transient
  `/ms-playwright-agent` directory which is then deleted. Consumers must
  `npm install playwright` (or `playwright-core`) themselves and run with
  `PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1` to reuse the bundled browsers.

## Test script

`/tmp/cspace-playwright-spike/test.mjs`:

```js
import { chromium } from 'playwright';
import { statSync } from 'node:fs';

const t0 = Date.now();
const browser = await chromium.launch();
const tLaunched = Date.now();

const page = await browser.newPage();
await page.goto('https://example.com');
const tNavigated = Date.now();

const title = await page.title();
const body = await page.locator('body').innerText();
await page.screenshot({ path: '/work/screenshot.png', fullPage: false });
const tScreenshot = Date.now();

await browser.close();

const result = {
  title,
  bodyExcerpt: body.slice(0, 200),
  screenshotBytes: statSync('/work/screenshot.png').size,
  timings: {
    launchMs: tLaunched - t0,
    navigateMs: tNavigated - tLaunched,
    screenshotMs: tScreenshot - tNavigated,
    totalMs: tScreenshot - t0,
  },
};

console.log(JSON.stringify(result, null, 2));
```

Companion `/tmp/cspace-playwright-spike/package.json`:

```json
{
  "name": "cspace-playwright-spike",
  "version": "0.0.0",
  "private": true,
  "type": "module",
  "dependencies": { "playwright": "1.58.0" }
}
```

Run command (final, working form):

```bash
container run --rm \
  --dns 8.8.8.8 \
  -v /tmp/cspace-playwright-spike:/work \
  --workdir /work \
  -e PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 \
  mcr.microsoft.com/playwright:v1.58.0-noble \
  bash -c 'npm install --omit=optional --no-audit --no-fund && node /work/test.mjs'
```

## Results

### Browser launch

```json
{
  "title": "Example Domain",
  "bodyExcerpt": "Example Domain\n\nThis domain is for use in documentation examples without needing permission. Avoid use in operations.\n\nLearn more",
  "screenshotBytes": 15907,
  "timings": {
    "launchMs": 228,
    "navigateMs": 121,
    "screenshotMs": 50,
    "totalMs": 399
  }
}
```

### Screenshot

- File created: yes
- Size: 15,907 bytes (16 KB on disk)
- Format: `PNG image data, 1280 x 720, 8-bit/color RGB, non-interlaced`
- Path: `/tmp/cspace-playwright-spike/screenshot.png`

### Boot + run timing

Measured wall-clock with `date +%s%N` around `container run --rm ...`:

| Scenario | Wall time | Notes |
| --- | --- | --- |
| First run incl. `npm install playwright@1.58.0` | ~3 s | Fresh `node_modules`, with `--dns 8.8.8.8`. |
| Cached run (`node_modules` already present) | **1.46 s** | Pure boot + Chromium launch + navigate + screenshot. |
| In-process Playwright totalMs (from script timings) | 0.40 s | Browser-side work only; the rest is microVM boot + Node startup + module load. |

So the Apple Container microVM cold-boot + Node import overhead is roughly
**1.0–1.1 s** before Playwright timings start ticking. Subsequent runs do
not get faster — every `--rm` invocation is a fresh microVM. To amortise
this, browser sessions should be long-lived inside one container, not
spawned per request.

## Verdict

- Chromium launches under Apple Container microVM: **PASS**
- Page navigation + DOM access: **PASS**
- Screenshot capture: **PASS**

Verdict: Apple Container is a viable substrate for headless Chromium via
Playwright, with one mandatory configuration detail (`--dns`).

## Surprises and gotchas

1. **DNS is broken by default for the Playwright image.** The container's
   `/etc/resolv.conf` points at `192.168.64.1` (the `container-network-vmnet`
   gateway, which is the host). For Alpine-based images in earlier spikes
   this path *worked*. For the Ubuntu Noble Playwright image, both
   `getent hosts registry.npmjs.org` and `chromium.goto('https://example.com')`
   fail with `EAI_AGAIN` / `net::ERR_NETWORK_CHANGED`. The fix is
   `--dns 8.8.8.8` (or any reachable resolver) on every `container run`.
   This is the same friction noted in the 2026-04-30 apple-container spike's
   "container DNS resolves through the gateway, sometimes flakily" finding,
   but here it manifests as a *hard* failure, not flake. Implication: cspace2
   provisioning must set `--dns` explicitly; we cannot rely on the gateway
   resolver for sandboxes that need outbound HTTPS (which is essentially
   all of them).
2. **Multi-arch pull.** `container image pull mcr.microsoft.com/playwright:v1.58.0-noble`
   unpacks **both** linux/amd64 and linux/arm64 layers (~4.7 GB total on
   disk) even though the host can only run one. There is no `--platform`
   flag on `image pull` in 0.12.3 — `container run` honors `--platform` but
   the pull cost is paid up front. For a fat sandbox image we'd ship via
   our own registry, this doubles storage on every dev machine.
3. **Image is "browsers without Playwright".** The image has Chromium binaries
   but no `node_modules/playwright`. You must `npm install playwright` (or
   `playwright-core`) yourself, and you **must** set
   `PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1` so the postinstall doesn't redownload
   ~500 MB of browsers it already has. This is by design upstream
   (multi-stage build hygiene) but easy to trip on.
4. **No `--shm-size` flag needed.** Apple Container does not expose a flag
   equivalent to Docker's `--shm-size`, but headless Chromium ran cleanly at
   default, including `page.locator('body').innerText()` and a 1280x720
   screenshot. We did not need `--disable-dev-shm-usage`. For complex pages
   this may not hold; flagging as a P2 watchpoint.
5. **MicroVM cold-boot floor is ~1.0 s.** Cached runs landed at 1.46 s
   wall-clock with Playwright timings of 0.40 s — so Apple Container's boot
   + init + Node startup is the dominant cost. This rules out a
   "spawn-per-request" model for browser tools; sessions must be persistent.
6. **Image is large.** ~2.5 GB unpacked per arch is heavy for a lean
   sandbox. Building a custom slim image (Chromium-only, no Firefox/WebKit)
   could drop ~1 GB; whether that's worth the maintenance burden depends on
   how often we'd rebuild it.

## Implications for P1+ sandbox image

Two paths, with concrete cost numbers from this spike:

- **Option A — Bake Playwright into Phase 1's sandbox image.** Extend
  `Dockerfile.cspace2` to install Chromium + playwright-core (mirroring
  Microsoft's recipe but trimming Firefox/WebKit). One container per
  sandbox; the supervisor speaks to a long-lived in-VM Playwright server.
  - Pros: single OCI image, no orchestration glue, one network namespace,
    no second microVM boot cost.
  - Cons: image grows by ~1 GB (Chromium + libs) for sandboxes that may
    never use a browser. Every cspace2 pull pays the disk cost.
- **Option B — Browser sidecar VM.** Keep `Dockerfile.cspace2` lean. For
  sandboxes that need a browser, launch a second top-level Apple Container
  running the Playwright image, talk to it over CDP via the
  `192.168.64.0/24` network. Two VMs per browser-needing sandbox.
  - Pros: cspace2 stays small; only browser-needing sandboxes pay the cost;
    upgrades to Chromium decouple from cspace2 image releases.
  - Cons: ~1.0 s extra cold-boot per sidecar; two VMs' worth of memory and
    iptables overhead per sandbox; needs DNS/hostname plumbing
    (`container system dns create` is admin-only, so we'd have to wire IPs
    or a custom resolver).

**Recommendation: Option B (sidecar), but defer the decision.** Reasoning:

- The spike showed that Apple Container's `--rm` boot floor is ~1.0 s. That
  is *cheap* — paying it once at sandbox spin-up to add a browser sidecar
  is acceptable. We do not gain meaningful speed by baking the browser in.
- Disk cost matters more than boot cost for prototype maintenance. Doubling
  cspace2's image size for a feature most coordinators won't use is a bad
  trade. The sidecar pattern matches today's docker-compose model
  (`devcontainer` + `cdp-chromium`) and the migration is mostly
  configuration, not architecture.
- However, the sibling-DNS limitation (no `getent hosts <name>`) means the
  supervisor must address the sidecar by IP. That IP is assigned at
  container start. We need a tiny piece of provisioning glue:
  `cspace up <sandbox>` runs the sidecar, captures its `192.168.64.x`
  address, and injects it as an env var into the supervisor. Workable; not
  free.
- If P2 finds the IP-injection glue to be too brittle or DNS-resolver
  setup proves required, we can fall back to Option A as a known-good
  retreat — the cost is one ~1 GB image rebuild.

## Deferred to P2 / browser implementation phase

- Per-session Chromium profile dirs (one-shot `--rm` runs leave nothing
  behind; we'll need a writable mount for cookies/storage state).
- Lazy-start the browser sidecar only when an MCP browser tool is invoked,
  to avoid paying the ~1 GB image and ~1.0 s boot for sandboxes that don't
  need it.
- CDP wiring from the supervisor / Claude Code MCP layer to the sidecar's
  `192.168.64.x:9222` (mirrors the existing compose `cdp-chromium` story).
- DNS provisioning policy: do we set `--dns 8.8.8.8` globally, route
  through a host-side resolver (e.g. dnsmasq on the gateway), or land
  `container system dns create` despite its admin requirement? This affects
  every cspace2 sandbox, not just browser-needing ones.
- Security: the firewall allowlist (`init-firewall.sh`) currently ends at
  GitHub/npm/Anthropic. A browser sandbox necessarily talks to arbitrary
  origins; we need a per-sandbox policy that broadens egress without
  weakening the supervisor's own egress rules.
- `--shm-size` analogue: revisit if Chromium SIGBUSes on real-world pages.
  Apple Container does not expose a flag today; the workaround would be
  `chromium.launch({ args: ['--disable-dev-shm-usage'] })`.
- Image trim: build a Chromium-only Playwright image (drop Firefox/WebKit,
  drop the `pwuser` apt overhead) if sidecar disk cost becomes an issue.
  Spike measured the official image at ~2.5 GB; a trim is plausibly under
  1 GB.

## Cleanup

- All `container run` invocations used `--rm`; no leftover containers.
- `/tmp/cspace-playwright-spike/` removed after report write.
