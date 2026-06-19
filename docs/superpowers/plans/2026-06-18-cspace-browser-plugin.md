# cspace-browser Plugin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Package cspace's browser MCP registration as an in-repo, conflict-scoped Claude Code plugin (`cspace-browser`) installed at container boot, replacing the imperative entrypoint `jq`-registration — on today's per-instance browser sidecar (Phase 1; Phase 2 swaps the sidecar to a shared singleton later).

**Architecture:** A config-only plugin (`lib/plugins/cspace-browser/`) declares two stdio MCP servers — `cspace-playwright` (`playwright-mcp --cdp-endpoint ${CSPACE_BROWSER_CDP_URL} --isolated`) and `cspace-chrome-devtools` (`chrome-devtools-mcp --browserUrl ${CSPACE_BROWSER_CDP_URL}`) — referencing the image-baked binaries. It ships via a local-path marketplace baked into the image and is installed at boot by `cspace-install-plugins.sh`, gated on a browser being present. The entrypoint `jq` block is removed; the headless supervisor's registration is reconciled by a spike.

**Tech Stack:** Claude Code plugins + local marketplace; bash (entrypoint + install script); Dockerfile (Apple Container builder); Go embedded assets (`make sync-embedded`); TypeScript/Bun (agent supervisor, `@anthropic-ai/claude-agent-sdk`).

## Global Constraints

- **Two copies of every `lib/runtime/` + `lib/` asset:** the source in `lib/…` AND the `go:embed` mirror in `internal/assets/embedded/…`. After editing any `lib/` file, run `make sync-embedded` (auto-run by `make build`) so the embedded mirror matches. The image ships the embedded copy.
- **Image is the only runtime source of truth.** Editing host `lib/` files does NOT change running containers; changes take effect only after `cspace image build` / `cspace rebuild` (`make cspace-image` for the maintainer fast-path).
- **MCP binary pins live in the Dockerfile**, not the plugin: `@playwright/mcp@0.0.72`, `chrome-devtools-mcp@0.23.0` (`lib/templates/Dockerfile:126-127`). The plugin references bare commands `playwright-mcp` / `chrome-devtools-mcp`.
- **CLI is `claude plugins` (plural)** — `claude plugins marketplace add`, `claude plugins install --scope user`, `claude plugins marketplace list` (match the existing `cspace-install-plugins.sh`).
- **Server names are `cspace-`prefixed** (`cspace-playwright`, `cspace-chrome-devtools`) — never bare `playwright`/`chrome-devtools` — because plugin MCP servers are the lowest precedence and a project's own `playwright` would silently shadow a bare-named one (spec § Server naming + collision avoidance).
- **Browser-presence gate:** the plugin is enabled iff `CSPACE_BROWSER_CDP_URL` is non-empty in the container env. `cmd_up.go` sets this both when cspace starts its sidecar (`cmd_up.go:542`) and when the project supplies its own CDP URL — so this gate needs **no Go change**.
- **Env-var expansion:** a bare `${CSPACE_BROWSER_CDP_URL}` with no default makes Claude Code **fail to parse** the whole `.mcp.json` if the var is unset. The gate guarantees it is set whenever the plugin is enabled; keep bare form (matches spec). If a real parse-fail ever surfaces, switch to `${CSPACE_BROWSER_CDP_URL:-http://localhost:9222}`.
- **Marketplace layout:** marketplace root = the dir containing `.claude-plugin/marketplace.json`; plugins are subdirs referenced by a `source` path starting with `./`. The plugin's own manifest lives at `<plugin>/.claude-plugin/plugin.json`; its `.mcp.json` lives at the plugin ROOT (sibling of `.claude-plugin/`, NOT inside it).

---

### Task 1: Create the plugin + local marketplace files

**Files:**
- Create: `lib/plugins/.claude-plugin/marketplace.json`
- Create: `lib/plugins/cspace-browser/.claude-plugin/plugin.json`
- Create: `lib/plugins/cspace-browser/.mcp.json`

**Interfaces:**
- Produces: a local marketplace named `cspace` exposing plugin `cspace-browser` (install id `cspace-browser@cspace`); MCP servers `cspace-playwright`, `cspace-chrome-devtools`.

- [ ] **Step 1: Write the marketplace manifest**

`lib/plugins/.claude-plugin/marketplace.json`:
```json
{
  "name": "cspace",
  "owner": { "name": "cspace" },
  "plugins": [
    {
      "name": "cspace-browser",
      "source": "./cspace-browser",
      "description": "Browser automation MCP servers (Playwright + Chrome DevTools) wired to the cspace Chromium CDP sidecar."
    }
  ]
}
```

- [ ] **Step 2: Write the plugin manifest**

`lib/plugins/cspace-browser/.claude-plugin/plugin.json`:
```json
{
  "name": "cspace-browser",
  "version": "0.1.0",
  "description": "cspace browser MCP servers (Playwright CDP-attached/isolated + Chrome DevTools), wired to the container's Chromium CDP sidecar via CSPACE_BROWSER_CDP_URL."
}
```

- [ ] **Step 3: Write the plugin MCP config**

`lib/plugins/cspace-browser/.mcp.json` (note: plugin ROOT, NOT inside `.claude-plugin/`):
```json
{
  "mcpServers": {
    "cspace-playwright": {
      "command": "playwright-mcp",
      "args": ["--cdp-endpoint", "${CSPACE_BROWSER_CDP_URL}", "--isolated"]
    },
    "cspace-chrome-devtools": {
      "command": "chrome-devtools-mcp",
      "args": ["--browserUrl", "${CSPACE_BROWSER_CDP_URL}"]
    }
  }
}
```

- [ ] **Step 4: Validate the JSON + required fields**

Run:
```bash
for f in lib/plugins/.claude-plugin/marketplace.json \
         lib/plugins/cspace-browser/.claude-plugin/plugin.json \
         lib/plugins/cspace-browser/.mcp.json; do jq -e . "$f" >/dev/null && echo "OK $f"; done
jq -e '.name and .owner.name and (.plugins | length > 0) and (.plugins[0].source | startswith("./"))' lib/plugins/.claude-plugin/marketplace.json >/dev/null && echo "marketplace fields OK"
jq -e '.name' lib/plugins/cspace-browser/.claude-plugin/plugin.json >/dev/null && echo "plugin name OK"
jq -e '.mcpServers."cspace-playwright".args | index("--isolated")' lib/plugins/cspace-browser/.mcp.json >/dev/null && echo "isolated flag present"
```
Expected: `OK …` for all three, `marketplace fields OK`, `plugin name OK`, `isolated flag present`.

- [ ] **Step 5: Validate with the plugin-dev validator**

Dispatch the `plugin-dev:plugin-validator` agent on `lib/plugins/cspace-browser/`. Expected: no structural errors (manifest present, `.mcp.json` at plugin root, valid marketplace). Fix anything it flags.

- [ ] **Step 6: Commit**

```bash
git add lib/plugins/
git commit -m "feat(plugin): add cspace-browser plugin + local marketplace files"
```

---

### Task 2: Ship the plugin into the image (Dockerfile COPY + embed sync)

**Files:**
- Modify: `Makefile` (the `sync-embedded` target, after the runtime-scripts copy block, ~line 26)
- Modify: `lib/templates/Dockerfile` (the `USER root` baking block, near the `/opt/cspace/features` COPY ~lines 168-170)

**Interfaces:**
- Consumes: `lib/plugins/` from Task 1.
- Produces: the marketplace baked at in-container path `/opt/cspace/plugins/` (root contains `.claude-plugin/marketplace.json`); embedded mirror at `internal/assets/embedded/plugins/`.

- [ ] **Step 1: Add a sync-embedded copy step for the plugin tree**

In `Makefile`, inside the `sync-embedded` target, after the existing `lib/runtime/scripts` copy lines, add:
```make
	@mkdir -p internal/assets/embedded/plugins
	@cp -R lib/plugins/. internal/assets/embedded/plugins/
```
(Use `cp -R lib/plugins/.` — the trailing `/.` copies contents including hidden `.claude-plugin/` dirs. A plain glob would miss dotfiles.)

- [ ] **Step 2: Run sync-embedded and verify the tree is mirrored**

Run:
```bash
make sync-embedded && find internal/assets/embedded/plugins -type f | sort
```
Expected (exactly these three files):
```
internal/assets/embedded/plugins/.claude-plugin/marketplace.json
internal/assets/embedded/plugins/cspace-browser/.claude-plugin/plugin.json
internal/assets/embedded/plugins/cspace-browser/.mcp.json
```

- [ ] **Step 3: Add the Dockerfile COPY + a loud build-time guard**

In `lib/templates/Dockerfile`, in the `USER root` baking section (mirroring the `/opt/cspace/features` pattern), add:
```dockerfile
# cspace-shipped Claude Code plugins (local marketplace source). Baked
# read-only; cspace-install-plugins.sh registers /opt/cspace/plugins as a
# filesystem marketplace (no network). The `RUN test` guard turns the known
# Apple Container "COPY <dir> yields empty dir" quirk into a loud build failure
# instead of a silent missing-plugin at runtime.
RUN mkdir -p /opt/cspace/plugins
COPY lib/plugins /opt/cspace/plugins
RUN test -f /opt/cspace/plugins/.claude-plugin/marketplace.json \
 && test -f /opt/cspace/plugins/cspace-browser/.mcp.json
```

- [ ] **Step 4: Sync embedded (Dockerfile is also mirrored) and build the image**

Run:
```bash
make sync-embedded
make cspace-image
```
Expected: build succeeds. If the `RUN test` step fails (empty dir from the COPY-dir quirk), replace the `COPY lib/plugins /opt/cspace/plugins` with explicit per-file COPYs (the workaround used by the supervisor stage — see `Dockerfile:24-29` comment):
```dockerfile
COPY lib/plugins/.claude-plugin/marketplace.json /opt/cspace/plugins/.claude-plugin/marketplace.json
COPY lib/plugins/cspace-browser/.claude-plugin/plugin.json /opt/cspace/plugins/cspace-browser/.claude-plugin/plugin.json
COPY lib/plugins/cspace-browser/.mcp.json /opt/cspace/plugins/cspace-browser/.mcp.json
```
then rebuild and re-verify.

- [ ] **Step 5: Verify the marketplace landed in the built image**

Run:
```bash
container run --rm cspace:latest sh -c 'cat /opt/cspace/plugins/.claude-plugin/marketplace.json && echo --- && cat /opt/cspace/plugins/cspace-browser/.mcp.json'
```
Expected: both files print their full JSON contents (not empty).

- [ ] **Step 6: Commit**

```bash
git add Makefile lib/templates/Dockerfile internal/assets/embedded/
git commit -m "build(image): bake cspace-browser plugin marketplace into the image"
```

---

### Task 3: Register + install the plugin at boot (gated on browser presence)

**Files:**
- Modify: `lib/runtime/scripts/cspace-install-plugins.sh` (insert a dedicated block immediately AFTER the `plugins.enabled` gate, ~after line 46, BEFORE the `WANT` discovery)
- Create: `lib/runtime/scripts/cspace-install-plugins.test.sh` (a stub-driven shell test)

**Interfaces:**
- Consumes: in-container `/opt/cspace/plugins` marketplace (Task 2); env `CSPACE_BROWSER_CDP_URL`.
- Produces: `claude plugins marketplace add /opt/cspace/plugins` + `claude plugins install --scope user cspace-browser@cspace` when a browser is present.

- [ ] **Step 1: Write the failing test (stub-driven)**

`lib/runtime/scripts/cspace-install-plugins.test.sh`:
```bash
#!/usr/bin/env bash
# Tests the cspace-browser block in cspace-install-plugins.sh using a `claude` stub.
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$HERE/cspace-install-plugins.sh"
fail() { echo "FAIL: $1"; exit 1; }

run_case() {  # $1=label  $2=cdp_url ("" = unset)  ; echoes the captured claude calls
  local tmp; tmp="$(mktemp -d)"
  mkdir -p "$tmp/bin" "$tmp/market/.claude-plugin"
  echo '{"name":"cspace","owner":{"name":"cspace"},"plugins":[{"name":"cspace-browser","source":"./cspace-browser"}]}' > "$tmp/market/.claude-plugin/marketplace.json"
  # stub `claude`: record args, print nothing for `marketplace list`
  cat > "$tmp/bin/claude" <<EOF
#!/usr/bin/env bash
echo "claude \$*" >> "$tmp/calls.log"
exit 0
EOF
  chmod +x "$tmp/bin/claude"
  HOME="$tmp" PATH="$tmp/bin:$PATH" \
    CSPACE_BROWSER_MARKET_DIR="$tmp/market" \
    CSPACE_BROWSER_CDP_URL="$2" \
    bash "$SCRIPT" >/dev/null 2>&1
  cat "$tmp/calls.log" 2>/dev/null || true
}

present="$(run_case present 'http://10.0.0.5:9222')"
echo "$present" | grep -q 'plugins marketplace add .*/market' || fail "present: marketplace not added"
echo "$present" | grep -q 'plugins install --scope user cspace-browser@cspace' || fail "present: plugin not installed"

absent="$(run_case absent '')"
echo "$absent" | grep -q 'cspace-browser@cspace' && fail "absent: plugin installed despite no CDP url"

echo "PASS"
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `chmod +x lib/runtime/scripts/cspace-install-plugins.test.sh && bash lib/runtime/scripts/cspace-install-plugins.test.sh`
Expected: FAIL (the cspace-browser block does not exist yet — "present: marketplace not added").

- [ ] **Step 3: Add the cspace-browser block to the install script**

In `lib/runtime/scripts/cspace-install-plugins.sh`, immediately after the `plugins.enabled` gate (the `fi` closing the block around line 46) and before the `declare -A WANT=()` discovery, insert:
```bash
# --- cspace-browser plugin (image-local marketplace) ---
# Register + install cspace's browser MCP plugin from the marketplace baked
# into the image. Gated on a browser sidecar being present
# (CSPACE_BROWSER_CDP_URL set) — this reproduces the old entrypoint behavior of
# not exposing browser tools that can't reach a CDP endpoint. Self-contained:
# does NOT go through the WANT/MARKETPLACES loop (that loop assumes GitHub
# owner/repo marketplaces and would mishandle a local filesystem path).
# Idempotent: `marketplace list` grep skips re-add; `plugins install` no-ops if
# already installed. CSPACE_BROWSER_MARKET_DIR is overridable for tests.
CSPACE_BROWSER_MARKET_DIR="${CSPACE_BROWSER_MARKET_DIR:-/opt/cspace/plugins}"
if [ -n "${CSPACE_BROWSER_CDP_URL:-}" ] && [ -f "${CSPACE_BROWSER_MARKET_DIR}/.claude-plugin/marketplace.json" ]; then
    if ! claude plugins marketplace list 2>/dev/null | grep -q "^[ *]*cspace\b"; then
        echo "[install-plugins] adding image-local marketplace cspace from ${CSPACE_BROWSER_MARKET_DIR}"
        claude plugins marketplace add "${CSPACE_BROWSER_MARKET_DIR}" || true
    fi
    echo "[install-plugins] installing cspace-browser@cspace"
    claude plugins install --scope user "cspace-browser@cspace" || true
fi
# --- end cspace-browser ---
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `bash lib/runtime/scripts/cspace-install-plugins.test.sh`
Expected: `PASS`.

- [ ] **Step 5: Lint the script + sync embedded**

Run:
```bash
bash -n lib/runtime/scripts/cspace-install-plugins.sh && echo "syntax OK"
make sync-embedded
diff -q lib/runtime/scripts/cspace-install-plugins.sh internal/assets/embedded/runtime/scripts/cspace-install-plugins.sh && echo "embedded mirror in sync"
```
Expected: `syntax OK`, `embedded mirror in sync`.

- [ ] **Step 6: Commit**

```bash
git add lib/runtime/scripts/cspace-install-plugins.sh lib/runtime/scripts/cspace-install-plugins.test.sh internal/assets/embedded/runtime/scripts/cspace-install-plugins.sh
git commit -m "feat(boot): install cspace-browser plugin when a browser sidecar is present"
```

---

### Task 4: Remove the imperative entrypoint browser-MCP registration

**Files:**
- Modify: `lib/runtime/scripts/cspace-entrypoint.sh` (delete the browser-MCP `jq` block, lines 105-149)

**Interfaces:**
- Consumes: nothing new.
- Produces: an entrypoint that no longer hand-writes `playwright`/`chrome-devtools` into `~/.claude.json` (the plugin from Task 1-3 supersedes it).

- [ ] **Step 1: Delete the browser-MCP jq block**

In `lib/runtime/scripts/cspace-entrypoint.sh`, delete lines 105-149 inclusive — the comment paragraph (105-124) plus the `if command -v jq … fi` block (125-149) that builds `BROWSER_MCP_FILTER` and mutates `$CLAUDE_JSON`. The region between the `.claude.json` seeding's closing `fi` (line 103) and the statusline-settings comment (line 151) becomes directly adjacent. Do NOT remove the `.claude.json` seeding block (63-103) or the statusline block — only the browser-MCP mutation.

- [ ] **Step 2: Verify the block is gone and nothing else regressed**

Run:
```bash
grep -n 'BROWSER_MCP_FILTER\|del(.mcpServers' lib/runtime/scripts/cspace-entrypoint.sh && echo "STILL PRESENT (bad)" || echo "browser jq block removed"
grep -c 'hasCompletedOnboarding' lib/runtime/scripts/cspace-entrypoint.sh   # .claude.json seeding still present -> expect 1
grep -c 'cspace-install-plugins.sh' lib/runtime/scripts/cspace-entrypoint.sh # install invocation still present -> expect >=1
bash -n lib/runtime/scripts/cspace-entrypoint.sh && echo "syntax OK"
```
Expected: `browser jq block removed`, the onboarding count `1`, install-invocation count `>=1`, `syntax OK`.

- [ ] **Step 3: Sync embedded**

Run:
```bash
make sync-embedded
diff -q lib/runtime/scripts/cspace-entrypoint.sh internal/assets/embedded/runtime/scripts/cspace-entrypoint.sh && echo "embedded mirror in sync"
```
Expected: `embedded mirror in sync`.

- [ ] **Step 4: Commit**

```bash
git add lib/runtime/scripts/cspace-entrypoint.sh internal/assets/embedded/runtime/scripts/cspace-entrypoint.sh
git commit -m "refactor(entrypoint): drop imperative browser-MCP jq registration (superseded by cspace-browser plugin)"
```

---

### Task 5: SPIKE — does the headless Agent SDK load enabled-plugin MCP servers?

**Files:** none modified (investigation). Record the outcome at the bottom of this plan and in the commit message of Task 6.

**Why:** The agent runs via the supervisor's `query()` from `@anthropic-ai/claude-agent-sdk` (`lib/agent-supervisor-bun/src/claude-runner.ts`), which today hardcodes the browser MCP servers (lines 61-76). If the SDK auto-loads enabled-plugin MCP servers, we can delete that block (Branch A); if not, we keep it but align names/flags (Branch B). The spike also prevents a double-registration collision (manual `cspace-playwright` + plugin `cspace-playwright`).

- [ ] **Step 1: Temporarily disable the manual registration**

In `lib/agent-supervisor-bun/src/claude-runner.ts`, comment out the entire `mcpServers: { … }` block (lines 61-76) so plugins are the only possible source of browser tools. Rebuild the supervisor and image:
```bash
make cspace-image
```

- [ ] **Step 2: Boot an instance with a browser and inspect the headless session's MCP servers**

Run (requires Apple Container + a configured Anthropic credential):
```bash
cspace up spike-mcp
cspace ssh spike-mcp -- sh -lc 'cat ~/.claude/cspace-install-plugins.log | grep -i cspace-browser'   # confirms the plugin installed
# Inspect what the headless supervisor session registered:
cspace ssh spike-mcp -- sh -lc 'ls /logs/events/spike-mcp/ && grep -o "mcp__[a-z_]*" /logs/events/spike-mcp/session-*.ndjson | sort -u | head'
```
Expected to reveal whether tool names of the form `mcp__plugin_cspace-browser_cspace-playwright__*` appear in the headless session's event log (plugin loaded by SDK) or whether NO browser tools appear (SDK does not load plugins).

- [ ] **Step 3: Drive a browser tool through the headless agent to confirm reachability**

Run:
```bash
cspace send spike-mcp "List your available MCP tools whose name contains 'browser' or 'playwright', then navigate to about:blank and report success."
cspace ssh spike-mcp -- sh -lc 'tail -40 /logs/events/spike-mcp/session-*.ndjson'
```
Decision criteria:
- **Branch A** if the agent lists/invokes `mcp__plugin_cspace-browser_cspace-playwright__*` tools successfully → the SDK loads plugin MCP servers.
- **Branch B** if the agent reports no browser tools (or only would-be `mcp__cspace-playwright__*` from manual registration, which is currently disabled) → the SDK does NOT load plugin MCP servers.

- [ ] **Step 4: Restore the manual block and record the decision**

Revert the Step-1 comment-out (`git checkout lib/agent-supervisor-bun/src/claude-runner.ts`). Tear down: `cspace down spike-mcp`. Append the result to the "Spike result" section at the bottom of this plan: **Branch A** or **Branch B**, with the observed tool names.

---

### Task 6: Reconcile the supervisor's MCP registration per the spike

**Files:**
- Modify: `lib/agent-supervisor-bun/src/claude-runner.ts` (lines 49-76)

**Interfaces:**
- Consumes: the Branch decision from Task 5.
- Produces: a single, collision-free source of headless browser tools.

#### If Branch A (SDK loads plugin servers):

- [ ] **Step A1: Delete the manual registration**

Remove the entire `mcpServers: { playwright: …, "chrome-devtools": … }` block (lines 61-76) and update the now-stale comment (lines 49-60) to: `// Browser MCP tools come from the enabled cspace-browser plugin (see lib/plugins/cspace-browser).`

- [ ] **Step A2: Build the supervisor and confirm no `mcpServers` browser keys remain**

Run:
```bash
grep -n 'playwright-mcp\|chrome-devtools-mcp' lib/agent-supervisor-bun/src/claude-runner.ts && echo "STILL PRESENT (bad)" || echo "manual registration removed"
cd lib/agent-supervisor-bun && pnpm install >/dev/null 2>&1 && pnpm exec tsc --noEmit && cd -
```
Expected: `manual registration removed`, and `tsc --noEmit` exits 0.

#### If Branch B (SDK does NOT load plugin servers):

- [ ] **Step B1: Rename keys to the cspace-prefixed names and add `--isolated`**

Replace the `mcpServers` block (lines 61-76) with:
```typescript
      mcpServers: {
        "cspace-playwright": {
          type: "stdio",
          command: "playwright-mcp",
          args: process.env.CSPACE_BROWSER_CDP_URL
            ? ["--isolated", "--cdp-endpoint", process.env.CSPACE_BROWSER_CDP_URL]
            : ["--isolated"],
        },
        "cspace-chrome-devtools": {
          type: "stdio",
          command: "chrome-devtools-mcp",
          args: process.env.CSPACE_BROWSER_CDP_URL
            ? ["--browserUrl", process.env.CSPACE_BROWSER_CDP_URL]
            : [],
        },
      },
```
Update the comment (lines 49-60) to note these match the cspace-browser plugin's server names/flags so the headless and interactive paths behave identically, and that the SDK does not auto-load plugin MCP servers (hence the explicit registration).

- [ ] **Step B2: Build the supervisor and confirm the rename**

Run:
```bash
grep -n '"cspace-playwright"\|"cspace-chrome-devtools"\|--isolated' lib/agent-supervisor-bun/src/claude-runner.ts && echo "renamed + isolated OK"
grep -n '\bplaywright:\|"chrome-devtools":' lib/agent-supervisor-bun/src/claude-runner.ts && echo "OLD BARE KEYS PRESENT (bad)" || echo "no bare keys"
cd lib/agent-supervisor-bun && pnpm install >/dev/null 2>&1 && pnpm exec tsc --noEmit && cd -
```
Expected: `renamed + isolated OK`, `no bare keys`, `tsc --noEmit` exits 0.

- [ ] **Step 3 (both branches): Sync embedded + commit**

Run:
```bash
make sync-embedded
git add lib/agent-supervisor-bun/src/claude-runner.ts internal/assets/embedded/
git commit -m "feat(supervisor): reconcile headless browser-MCP registration with cspace-browser plugin (Branch <A|B>)"
```

---

### Task 7: Integration verification (two instances, isolation, no regression)

**Files:** none modified (acceptance test).

**Interfaces:**
- Consumes: the fully built image from Tasks 1-6.

- [ ] **Step 1: Rebuild and boot two instances with browsers**

Run:
```bash
make cspace-image
cspace up alpha
cspace up beta
```
Expected: both boot; `cspace ssh alpha -- sh -lc 'cat ~/.claude/cspace-install-plugins.log'` shows `installing cspace-browser@cspace`.

- [ ] **Step 2: Confirm the plugin's servers are registered and cspace-prefixed**

Run:
```bash
cspace ssh alpha -- sh -lc 'claude plugins list 2>/dev/null | grep -i cspace-browser'
cspace ssh alpha -- sh -lc 'claude mcp list 2>/dev/null | grep -i cspace-'
```
Expected: `cspace-browser` listed as installed; the MCP list shows `cspace-playwright` / `cspace-chrome-devtools` (NOT bare `playwright`/`chrome-devtools`).

- [ ] **Step 3: Confirm playwright runs with `--isolated` and reaches the sidecar**

Run:
```bash
cspace send alpha "Use your cspace-playwright browser tool to navigate to data:text/html,<title>alpha</title> and report the page title."
cspace ssh alpha -- sh -lc 'tail -30 /logs/events/alpha/session-*.ndjson'
```
Expected: the agent reports title `alpha`; no "failed to launch chromium" error (it attaches to the sidecar CDP, not a local browser).

- [ ] **Step 4: Confirm cross-instance Playwright isolation (cookies do not leak)**

Run:
```bash
cspace send alpha "With cspace-playwright, navigate to https://example.com, set a cookie name=who value=ALPHA, and confirm it is set."
cspace send beta  "With cspace-playwright, navigate to https://example.com and read back the 'who' cookie; report its value or 'none'."
cspace ssh beta -- sh -lc 'tail -30 /logs/events/beta/session-*.ndjson'
```
Expected: beta reports `none` (alpha's cookie is NOT visible) — confirms `--isolated` gives per-instance contexts even though (in Phase 2) they'd share one browser. (Phase 1 per-instance browsers also isolate; this guards the flag is actually applied.)

- [ ] **Step 5: Negative check — `--no-browser` instance has no browser plugin**

Run:
```bash
cspace up gamma --no-browser
cspace ssh gamma -- sh -lc 'claude plugins list 2>/dev/null | grep -i cspace-browser && echo "INSTALLED (bad)" || echo "not installed (correct)"'
```
Expected: `not installed (correct)` — the `CSPACE_BROWSER_CDP_URL` gate skipped it.

- [ ] **Step 6: Tear down + final commit (docs/status only if needed)**

```bash
cspace down alpha; cspace down beta; cspace down gamma
git commit --allow-empty -m "test: verify cspace-browser plugin registration, isolation, and no-browser gating"
```

---

## Spike result

_(Filled in during Task 5.)_

- **Branch chosen:** TBD (A = SDK loads plugin servers / B = SDK needs manual registration)
- **Observed headless tool names:** TBD
- **Notes:** TBD
