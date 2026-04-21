# Advisor Agents Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a pluggable long-running advisor-agent class, with the decision-maker (Opus, max effort) as the first instance, and move inter-agent messaging from bash `cspace send` to typed role-scoped MCP tools.

**Architecture:** Each advisor runs in its own cspace container as a `role=advisor` supervisor (persistent, session-continuous). Advisors are declared in `.cspace.json` under `advisors`. The existing in-process `agent-messenger` MCP server gains role-scoped send/ask/reply tools; the `status` socket command gains `git_branch` and `queue_depth`. `cspace coordinate` auto-launches configured advisors alongside the coordinator.

**Tech Stack:** Go (CLI, config, supervisor launch), Node.js ESM (agent-supervisor, MCP tools via Claude Agent SDK + Zod), markdown (playbooks).

**Spec:** See `docs/superpowers/specs/2026-04-18-advisor-agents-design.md` for full design rationale.

---

## File Structure

**New files:**
- `lib/advisors/decision-maker.md` — shipped system prompt for the decision-maker advisor
- `internal/advisor/advisor.go` — Go package: advisor launch, liveness probe, teardown
- `internal/advisor/advisor_test.go` — tests for advisor package
- `internal/cli/advisor.go` — `cspace advisor list|down|restart` subcommands

**Modified files:**
- `lib/defaults.json` — add `advisors` block with decision-maker default
- `internal/config/config.go` — `Advisors map[string]AdvisorConfig` on `Config`, `AdvisorConfig` struct
- `internal/config/config_test.go` — test parsing of advisors block
- `internal/config/resolve.go` — `ResolveAdvisor(name)` helper
- `internal/config/resolve_test.go` — test resolve advisor
- `internal/supervisor/dispatch.go` — add `RoleAdvisor = "advisor"` constant
- `internal/supervisor/launch.go` — thread advisor role through `LaunchParams` (model/effort overrides)
- `internal/supervisor/launch_test.go` — test advisor launch args
- `lib/agent-supervisor/args.mjs` — accept `--role advisor`
- `lib/agent-supervisor/supervisor.mjs` — role=advisor multi-turn semantics, extended `status` response, `shutdown_self` socket command
- `lib/agent-supervisor/supervisor.test.mjs` — tests for status extension and shutdown_self
- `lib/agent-supervisor/sdk-mcp-tools.mjs` — new role-scoped send/ask/reply tools
- `lib/agent-supervisor/sdk-mcp-tools.test.mjs` — new test file for messaging tools (create)
- `internal/cli/coordinate.go` — launch advisors on startup, Sonnet default, render roster into prompt
- `internal/cli/root.go` — register `cspace advisor` command
- `lib/agents/coordinator.md` — Phase 0.5 (advisors), worker `--persistent` launch, triggers
- `lib/agents/implementer.md` — handshake at Setup, `ask_advisor` mid-task, `shutdown_self` at Ship
- `CLAUDE.md` — "Advisors" section

---

## Conventions for this plan

- Go tests use `go test ./...` or scoped forms like `go test ./internal/config/...`.
- Node tests use `cd lib/agent-supervisor && node --test <file>`. The supervisor package already uses `node --test` with no extra test runner.
- After each task, run `make vet` and `make test` from the repo root as a sanity check. Tasks reference `make sync-embedded` when assets change — `make build` does this automatically but tests that read embedded assets need it explicitly.
- Commit messages follow the repo style: short imperative, no trailing period. E.g. `Add AdvisorConfig struct and Advisors map to Config`.

---

### Task 1: Add `AdvisorConfig` struct and `Advisors` map to `Config`

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/config/config_test.go`:

```go
func TestConfigAdvisorsBlock(t *testing.T) {
	cfg, err := loadConfigFromJSON(t, `{
		"advisors": {
			"decision-maker": {
				"model": "claude-opus-4-7",
				"effort": "max",
				"baseBranch": "main"
			},
			"custom": {
				"systemPromptFile": ".cspace/advisors/custom.md"
			}
		}
	}`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(cfg.Advisors) != 2 {
		t.Fatalf("want 2 advisors, got %d", len(cfg.Advisors))
	}
	dm, ok := cfg.Advisors["decision-maker"]
	if !ok {
		t.Fatalf("missing decision-maker entry")
	}
	if dm.Model != "claude-opus-4-7" {
		t.Errorf("Model = %q, want claude-opus-4-7", dm.Model)
	}
	if dm.Effort != "max" {
		t.Errorf("Effort = %q, want max", dm.Effort)
	}
	if dm.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q, want main", dm.BaseBranch)
	}
	custom := cfg.Advisors["custom"]
	if custom.SystemPromptFile != ".cspace/advisors/custom.md" {
		t.Errorf("SystemPromptFile = %q", custom.SystemPromptFile)
	}
}

func TestConfigAdvisorsEmptyByDefault(t *testing.T) {
	cfg, err := loadConfigFromJSON(t, `{}`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Advisors == nil {
		t.Fatal("Advisors should be non-nil (possibly empty map from defaults.json)")
	}
}
```

If `loadConfigFromJSON` does not yet exist, add this helper at the top of the test file (check if a similar helper already exists first):

```go
func loadConfigFromJSON(t *testing.T, overlay string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".cspace.json"), []byte(overlay), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(dir, "")
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/config/ -run TestConfigAdvisors -v
```

Expected: FAIL with `cfg.Advisors undefined`.

- [ ] **Step 3: Add the struct and field**

In `internal/config/config.go`, add above the existing `ProjectConfig` struct definition (or wherever related types live):

```go
// AdvisorConfig configures a single long-running advisor agent.
// See docs/superpowers/specs/2026-04-18-advisor-agents-design.md.
type AdvisorConfig struct {
	Model            string `json:"model,omitempty"`
	Effort           string `json:"effort,omitempty"`
	SystemPromptFile string `json:"systemPromptFile,omitempty"`
	BaseBranch       string `json:"baseBranch,omitempty"`
}
```

In the `Config` struct (currently ending around line 48), add the field immediately after `Plugins`:

```go
Advisors map[string]AdvisorConfig `json:"advisors,omitempty"`
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/config/ -run TestConfigAdvisors -v
```

Expected: PASS (both subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "Add AdvisorConfig struct and Advisors map to Config"
```

---

### Task 2: Add `ResolveAdvisor` helper

**Files:**
- Modify: `internal/config/resolve.go`
- Test: `internal/config/resolve_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/config/resolve_test.go`:

```go
func TestResolveAdvisor(t *testing.T) {
	project := t.TempDir()
	assets := t.TempDir()

	// Fallback: assets/advisors/decision-maker.md exists.
	if err := os.MkdirAll(filepath.Join(assets, "advisors"), 0o755); err != nil {
		t.Fatal(err)
	}
	fallback := filepath.Join(assets, "advisors", "decision-maker.md")
	if err := os.WriteFile(fallback, []byte("fallback"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := ResolveAdvisor(project, assets, "decision-maker")
	if got != fallback {
		t.Errorf("fallback: got %s, want %s", got, fallback)
	}

	// Override: .cspace/advisors/decision-maker.md wins.
	if err := os.MkdirAll(filepath.Join(project, ".cspace", "advisors"), 0o755); err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(project, ".cspace", "advisors", "decision-maker.md")
	if err := os.WriteFile(override, []byte("override"), 0o644); err != nil {
		t.Fatal(err)
	}

	got = ResolveAdvisor(project, assets, "decision-maker")
	if got != override {
		t.Errorf("override: got %s, want %s", got, override)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/config/ -run TestResolveAdvisor -v
```

Expected: FAIL with `undefined: ResolveAdvisor`.

- [ ] **Step 3: Add the resolver**

In `internal/config/resolve.go`, add after `ResolveAgent`:

```go
// ResolveAdvisor resolves an advisor system-prompt file, checking the project
// override directory first, then falling back to the extracted embedded assets.
//
// Resolution order:
//  1. $PROJECT_ROOT/.cspace/advisors/<name>.md
//  2. $ASSETS_DIR/advisors/<name>.md
//
// Used when AdvisorConfig.SystemPromptFile is empty (the default). Callers
// that want to respect an explicit SystemPromptFile should check that first.
func ResolveAdvisor(projectRoot, assetsDir, name string) string {
	path, _ := resolveFile(projectRoot, assetsDir, "advisors", "advisors", name+".md")
	return path
}
```

And add a `Config` method below the existing `ResolveAgent` method wrapper:

```go
// ResolveAdvisor resolves an advisor system-prompt file using this config's paths.
// If the named advisor's config has an explicit SystemPromptFile, that path is
// returned directly (joined against project root if relative); otherwise the
// default override/fallback resolution applies.
func (c *Config) ResolveAdvisor(name string) string {
	if a, ok := c.Advisors[name]; ok && a.SystemPromptFile != "" {
		if filepath.IsAbs(a.SystemPromptFile) {
			return a.SystemPromptFile
		}
		return filepath.Join(c.ProjectRoot, a.SystemPromptFile)
	}
	return ResolveAdvisor(c.ProjectRoot, c.AssetsDir, name)
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/config/ -run TestResolveAdvisor -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/resolve.go internal/config/resolve_test.go
git commit -m "Add ResolveAdvisor for advisor system-prompt path resolution"
```

---

### Task 3: Ship default decision-maker system prompt and defaults.json entry

**Files:**
- Create: `lib/advisors/decision-maker.md`
- Modify: `lib/defaults.json`

No TDD here — this is shipped content, not executable behavior. Task 1's test already covers defaults merging.

- [ ] **Step 1: Create the shipped system prompt**

Create `lib/advisors/decision-maker.md` with exactly this content:

```markdown
You are a decision-making consultant to the cspace coordinator and
implementer agents. You do not write code. You read, reason, and reply.

## Your job
Weigh architectural trade-offs against the project's stated principles
and direction. When consulted, produce a recommendation with explicit
reasoning.

## On each consultation
1. Call read_context(["direction","principles","roadmap"]) for fresh
   values (humans edit these; your session cache may be stale).
2. Call list_findings(status=["open","acknowledged"]) and read any that
   bear on the question.
3. Call list_entries(kind="decisions") and read any prior decisions that
   touch the same area.
4. Read code as needed — grep, read, follow references.

## Response shape
- Recommendation (one sentence).
- Key reasoning (3-8 bullets, each tied to a principle, constraint, or
  prior decision).
- Alternatives considered and why they lose.
- Follow-ups for the caller if any.

## Record your conclusions
For non-trivial calls, call log_decision(...) so the reasoning survives
beyond your session. The coordinator/implementer reading it later should
be able to act without re-consulting you.

## On handshakes
If the message is a handshake_advisor (an implementer saying "starting
work on X"), do a shallow research pass: read the issue, grep the hinted
files, skim related decisions/findings. Do not reply to the implementer.
Your SDK session now has that context and will be warm for later questions.

The note_to_coordinator tool is available if during research you discover
something the coordinator needs to know right away (a conflict with a
prior decision, a finding that invalidates the issue's premise). Use it
sparingly — the default on handshakes is silence.

## Anti-patterns
- Do not edit code, open PRs, run verify commands, or take side effects
  beyond context-server writes.
- Do not answer questions that aren't architectural — redirect to the
  coordinator.
- Do not speculate past what principles.md and direction.md actually say.
  If they're silent on a question, say so explicitly.
```

- [ ] **Step 2: Add advisors block to defaults.json**

In `lib/defaults.json`, after the `plugins` block and before `services`, insert:

```json
  "advisors": {
    "decision-maker": {
      "model": "claude-opus-4-7",
      "effort": "max",
      "baseBranch": "main"
    }
  },
```

Verify it's still valid JSON:

```bash
python3 -m json.tool < lib/defaults.json > /dev/null && echo "valid JSON"
```

Expected: `valid JSON`.

- [ ] **Step 3: Sync embedded assets**

```bash
make sync-embedded
```

Expected: no errors; embedded copy of `lib/` refreshed.

- [ ] **Step 4: Verify defaults are loaded**

```bash
go test ./internal/config/... -v
```

Expected: all tests pass. Existing parse tests see the new `advisors` block without issue.

- [ ] **Step 5: Commit**

```bash
git add lib/advisors/decision-maker.md lib/defaults.json internal/assets/embedded
git commit -m "Ship default decision-maker system prompt and defaults.json advisors block"
```

---

### Task 4: Extend supervisor `status` with `git_branch` and `queue_depth`

**Files:**
- Modify: `lib/agent-supervisor/supervisor.mjs`
- Test: `lib/agent-supervisor/supervisor.test.mjs`

The current `status` reply is `{ role, instance, sessionId, turns, lastActivityMs }`. We add `git_branch` (cached 2s) and `queue_depth`.

- [ ] **Step 1: Write the failing test**

Append to `lib/agent-supervisor/supervisor.test.mjs` (if the file exists) or create it with:

```javascript
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { computeStatusExtras } from './supervisor.mjs'

test('computeStatusExtras returns queue_depth and git_branch', async () => {
  const extras = await computeStatusExtras({
    queueLength: 3,
    cwd: process.cwd(),
  })
  assert.equal(extras.queue_depth, 3)
  assert.equal(typeof extras.git_branch, 'string')
})

test('computeStatusExtras caches git_branch within TTL', async () => {
  const state = { lastBranch: null, lastBranchTs: 0 }
  const first = await computeStatusExtras({
    queueLength: 0,
    cwd: process.cwd(),
    cache: state,
    now: () => 1000,
  })
  assert.ok(state.lastBranchTs === 1000)
  const second = await computeStatusExtras({
    queueLength: 0,
    cwd: '/does/not/exist',
    cache: state,
    now: () => 1500, // within 2s TTL
  })
  assert.equal(second.git_branch, first.git_branch, 'should reuse cached branch')
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd lib/agent-supervisor && node --test supervisor.test.mjs
```

Expected: FAIL with `computeStatusExtras is not a function`.

- [ ] **Step 3: Implement `computeStatusExtras`**

In `lib/agent-supervisor/supervisor.mjs`, add above the `startSocket` function:

```javascript
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'

const execFileAsync = promisify(execFile)

const GIT_BRANCH_TTL_MS = 2000

/**
 * Build the extra fields to merge into the supervisor's status reply.
 * `cache` is a mutable object ({lastBranch, lastBranchTs}) used to cache
 * the git branch result across rapid-fire calls. `now` is injectable for tests.
 */
export async function computeStatusExtras({ queueLength, cwd, cache, now }) {
  cache = cache || computeStatusExtras.defaultCache
  now = now || Date.now

  const extras = { queue_depth: queueLength }

  const t = now()
  if (cache.lastBranch !== null && t - cache.lastBranchTs < GIT_BRANCH_TTL_MS) {
    extras.git_branch = cache.lastBranch
  } else {
    let branch = 'unknown'
    try {
      const { stdout } = await execFileAsync('git', ['-C', cwd, 'rev-parse', '--abbrev-ref', 'HEAD'], {
        timeout: 1000,
      })
      branch = stdout.trim() || 'unknown'
    } catch {
      branch = 'unknown'
    }
    cache.lastBranch = branch
    cache.lastBranchTs = t
    extras.git_branch = branch
  }

  return extras
}
computeStatusExtras.defaultCache = { lastBranch: null, lastBranchTs: 0 }
```

- [ ] **Step 4: Wire extras into the status socket reply**

In `supervisor.mjs`, find the `status` case inside `handleRequest`:

```javascript
case 'status':
  return { ok: true, status: getStatus() }
```

Replace with:

```javascript
case 'status': {
  const base = getStatus()
  const extras = await computeStatusExtras({
    queueLength: promptQueue._queue.length,
    cwd: cwd,
  })
  return { ok: true, status: { ...base, ...extras } }
}
```

Note: this requires `cwd` and `promptQueue` to be in scope where `handleRequest` is defined. `startSocket` already takes `promptQueue` as a parameter. Update `startSocket`'s signature to also accept `cwd`, and thread it from the `main()` call site.

Update the `startSocket({ sockPath, promptQueue, getQuery, getStatus, onShutdown })` signature to:

```javascript
function startSocket({ sockPath, promptQueue, cwd, getQuery, getStatus, onShutdown }) {
```

And update the `main()` call site (search for `const sockServer = startSocket({`) to pass `cwd`:

```javascript
const sockServer = startSocket({
  sockPath,
  promptQueue,
  cwd,
  getQuery: () => queryHandle,
  ...
```

- [ ] **Step 5: Run test to verify it passes**

```bash
cd lib/agent-supervisor && node --test supervisor.test.mjs
```

Expected: PASS (both tests).

- [ ] **Step 6: Commit**

```bash
git add lib/agent-supervisor/supervisor.mjs lib/agent-supervisor/supervisor.test.mjs
git commit -m "Extend supervisor status socket with queue_depth and cached git_branch"
```

---

### Task 5: Add `shutdown_self` socket command

**Files:**
- Modify: `lib/agent-supervisor/supervisor.mjs`
- Test: `lib/agent-supervisor/supervisor.test.mjs`

The existing `shutdown` socket command already works. We're adding a named alias tailored for agent self-shutdown so the MCP tool layer can expose it with a clean name. For now, alias `shutdown_self` to the same path as `shutdown`.

- [ ] **Step 1: Write the failing test**

Append to `lib/agent-supervisor/supervisor.test.mjs`:

```javascript
import { handleSupervisorRequest } from './supervisor.mjs'

test('shutdown_self closes the prompt queue', async () => {
  let shutdownCalled = false
  const state = {
    promptQueue: { push: () => {}, close: () => {}, _queue: [] },
    cwd: process.cwd(),
    onShutdown: () => { shutdownCalled = true },
    getQuery: () => null,
    getStatus: () => ({ role: 'agent', instance: 'test', sessionId: '', turns: 0, lastActivityMs: 0 }),
  }
  const reply = await handleSupervisorRequest({ cmd: 'shutdown_self' }, state)
  assert.equal(reply.ok, true)
  assert.equal(shutdownCalled, true)
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd lib/agent-supervisor && node --test supervisor.test.mjs
```

Expected: FAIL with `handleSupervisorRequest is not a function`.

- [ ] **Step 3: Extract `handleSupervisorRequest` as an exported pure-ish function**

In `supervisor.mjs`, extract the `handleRequest` logic inside `startSocket` into a top-level exported function. Replace the inline definition with a call to the exported one.

Add near the top of the file (after helpers):

```javascript
/**
 * Handle a single supervisor socket request. Exported for tests.
 * `state` = { promptQueue, cwd, getQuery, getStatus, onShutdown }
 */
export async function handleSupervisorRequest(req, state) {
  switch (req.cmd) {
    case 'send_user_message': {
      if (typeof req.text !== 'string' || !req.text) {
        return { ok: false, error: 'text required' }
      }
      state.promptQueue.push(makeUserMessage(req.text))
      return { ok: true }
    }
    case 'interrupt': {
      const q = state.getQuery()
      if (!q) return { ok: false, error: 'query not yet started' }
      try {
        await q.interrupt()
        return { ok: true }
      } catch (e) {
        return { ok: false, error: e.message }
      }
    }
    case 'status': {
      const base = state.getStatus()
      const extras = await computeStatusExtras({
        queueLength: state.promptQueue._queue.length,
        cwd: state.cwd,
      })
      return { ok: true, status: { ...base, ...extras } }
    }
    case 'shutdown':
    case 'shutdown_self':
      state.onShutdown()
      return { ok: true }
    default:
      return { ok: false, error: `unknown cmd: ${req.cmd}` }
  }
}
```

Inside `startSocket`, replace the existing inline `handleRequest` with a wrapper that delegates:

```javascript
async function handleRequest(req) {
  return handleSupervisorRequest(req, {
    promptQueue,
    cwd,
    getQuery,
    getStatus,
    onShutdown,
  })
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd lib/agent-supervisor && node --test supervisor.test.mjs
```

Expected: PASS (all tests including prior `computeStatusExtras` ones).

- [ ] **Step 5: Commit**

```bash
git add lib/agent-supervisor/supervisor.mjs lib/agent-supervisor/supervisor.test.mjs
git commit -m "Extract handleSupervisorRequest and add shutdown_self command alias"
```

---

### Task 6: Add shared MCP-tool helpers (socket send+status, envelope builder, error envelope)

**Files:**
- Modify: `lib/agent-supervisor/sdk-mcp-tools.mjs`
- Create: `lib/agent-supervisor/sdk-mcp-tools.test.mjs`

Adds internal helpers that all subsequent send/ask/reply tools use. No tool registration yet.

- [ ] **Step 1: Write the failing test**

Create `lib/agent-supervisor/sdk-mcp-tools.test.mjs`:

```javascript
import { test } from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import fs from 'node:fs'
import path from 'node:path'
import os from 'node:os'
import {
  sendAndFetchStatus,
  buildDeliveredEnvelope,
  buildErrorEnvelope,
} from './sdk-mcp-tools.mjs'

function startFakeSupervisor(sockPath, statusReply) {
  const srv = net.createServer((conn) => {
    let buf = ''
    conn.on('data', (chunk) => {
      buf += chunk.toString('utf-8')
      while (true) {
        const idx = buf.indexOf('\n')
        if (idx < 0) break
        const line = buf.slice(0, idx)
        buf = buf.slice(idx + 1)
        const req = JSON.parse(line)
        if (req.cmd === 'send_user_message') {
          conn.write(JSON.stringify({ ok: true }) + '\n')
        } else if (req.cmd === 'status') {
          conn.write(JSON.stringify({ ok: true, status: statusReply }) + '\n')
        }
      }
    })
  })
  return new Promise((resolve) => srv.listen(sockPath, () => resolve(srv)))
}

test('sendAndFetchStatus delivers message and returns status', async () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'sup-'))
  const sockPath = path.join(tmp, 'supervisor.sock')
  const statusReply = {
    role: 'agent',
    instance: 'decision-maker',
    sessionId: 'abc',
    turns: 7,
    lastActivityMs: 10_000,
    queue_depth: 1,
    git_branch: 'main',
  }
  const srv = await startFakeSupervisor(sockPath, statusReply)
  try {
    const r = await sendAndFetchStatus(sockPath, 'hello')
    assert.equal(r.delivered, true)
    assert.equal(r.status.instance, 'decision-maker')
    assert.equal(r.status.git_branch, 'main')
  } finally {
    srv.close()
  }
})

test('sendAndFetchStatus returns delivered:false for missing socket', async () => {
  const r = await sendAndFetchStatus('/tmp/does-not-exist-xyzzy.sock', 'hello')
  assert.equal(r.delivered, false)
  assert.match(r.error, /not present|connect|ENOENT/)
})

test('buildDeliveredEnvelope shape', () => {
  const env = buildDeliveredEnvelope({
    recipient: 'decision-maker',
    status: { git_branch: 'main', turns: 5, lastActivityMs: 1000, queue_depth: 0, sessionId: 's' },
    expectedReplyWindow: '~2-10 min',
    guidance: 'Continue your current task.',
  })
  assert.equal(env.delivered, true)
  assert.equal(env.recipient, 'decision-maker')
  assert.equal(env.recipient_status.git_branch, 'main')
  assert.equal(env.recipient_status.idle_for_seconds, 1)
  assert.equal(env.expected_reply_window, '~2-10 min')
})

test('buildErrorEnvelope shape', () => {
  const env = buildErrorEnvelope({
    recipient: 'gone',
    sockPath: '/tmp/gone.sock',
    reason: 'socket not present',
  })
  assert.equal(env.delivered, false)
  assert.equal(env.recipient, 'gone')
  assert.match(env.error, /not reachable/)
  assert.match(env.suggestion, /restart/)
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd lib/agent-supervisor && node --test sdk-mcp-tools.test.mjs
```

Expected: FAIL with named exports missing.

- [ ] **Step 3: Implement helpers**

In `lib/agent-supervisor/sdk-mcp-tools.mjs`, after the existing `trySocketRequest` / `sockPathFor` helpers, add:

```javascript
/**
 * Send a user message to a supervisor's socket and then probe its status.
 * Returns `{delivered, status, error?}`. On delivery failure (no socket,
 * bad reply), returns `{delivered: false, error: <reason>}`.
 */
export async function sendAndFetchStatus(sockPath, text) {
  const sendReply = await trySocketRequest(sockPath, { cmd: 'send_user_message', text })
  if (!sendReply.ok) {
    return { delivered: false, error: sendReply.error || 'send failed' }
  }
  const statusReply = await trySocketRequest(sockPath, { cmd: 'status' })
  if (!statusReply.ok || !statusReply.status) {
    return { delivered: true, status: null, error: statusReply.error || 'status unavailable' }
  }
  return { delivered: true, status: statusReply.status }
}

/**
 * Build the standard send-tool return envelope for a successful delivery.
 */
export function buildDeliveredEnvelope({ recipient, status, expectedReplyWindow, guidance }) {
  const recipientStatus = status
    ? {
        git_branch: status.git_branch || 'unknown',
        turns_completed: status.turns ?? 0,
        idle_for_seconds: Math.round((status.lastActivityMs ?? 0) / 1000),
        queue_depth: status.queue_depth ?? 0,
        session_id: status.sessionId || null,
      }
    : null
  return {
    delivered: true,
    recipient,
    recipient_status: recipientStatus,
    expected_reply_window: expectedReplyWindow,
    guidance,
  }
}

/**
 * Build the standard send-tool return envelope for a delivery failure.
 */
export function buildErrorEnvelope({ recipient, sockPath, reason }) {
  return {
    delivered: false,
    recipient,
    error: `recipient's supervisor not reachable at ${sockPath} (${reason})`,
    suggestion: `restart the recipient (e.g. \`cspace advisor restart ${recipient}\` for advisors, or \`cspace restart-supervisor ${recipient}\` for workers)`,
  }
}

/**
 * Wrap the envelope in the { content: [{type: 'text', text: ...}] }
 * shape that MCP tools return.
 */
export function toolResult(envelope) {
  return {
    content: [{ type: 'text', text: JSON.stringify(envelope, null, 2) }],
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
cd lib/agent-supervisor && node --test sdk-mcp-tools.test.mjs
```

Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add lib/agent-supervisor/sdk-mcp-tools.mjs lib/agent-supervisor/sdk-mcp-tools.test.mjs
git commit -m "Add MCP-tool helpers for socket send+status and envelope building"
```

---

### Task 7: Expose advisor recipient enum at supervisor startup

**Files:**
- Modify: `lib/agent-supervisor/args.mjs` (new `--advisors` flag)
- Modify: `lib/agent-supervisor/supervisor.mjs` (read flag, pass to MCP builder)
- Modify: `lib/agent-supervisor/sdk-mcp-tools.mjs` (accept `advisorNames` parameter)

The MCP tool schemas need an enum of advisor names. Since `sdk-mcp-tools.mjs` is purely Node (no cspace config access), the Go launcher passes the list via a new CLI flag.

- [ ] **Step 1: Write the failing test**

Append to `lib/agent-supervisor/args.test.mjs` (file exists or creatable):

```javascript
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { parseArgsForTest } from './args.mjs'

test('--advisors accepts comma-separated list', () => {
  const args = parseArgsForTest([
    'node', 'supervisor.mjs',
    '--role', 'agent',
    '--instance', 'issue-42',
    '--prompt-file', '/tmp/p',
    '--advisors', 'decision-maker,critic',
  ])
  assert.deepEqual(args.advisors, ['decision-maker', 'critic'])
})

test('--advisors absent yields empty list', () => {
  const args = parseArgsForTest([
    'node', 'supervisor.mjs',
    '--role', 'agent',
    '--instance', 'issue-42',
    '--prompt-file', '/tmp/p',
  ])
  assert.deepEqual(args.advisors, [])
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd lib/agent-supervisor && node --test args.test.mjs
```

Expected: FAIL.

- [ ] **Step 3: Add the flag to parser**

In `lib/agent-supervisor/args.mjs`:

- In the initial `args` object, add `advisors: []`.
- In the parse loop, add:

```javascript
else if (arg === '--advisors') {
  const val = argv[++i] || ''
  args.advisors = val.split(',').map((s) => s.trim()).filter(Boolean)
}
```

- In the help text, add `  --advisors <csv>              Comma-separated advisor names (populates MCP tool enums).`

- [ ] **Step 4: Thread through `supervisor.mjs`**

In `supervisor.mjs`, find `buildMessengerMcpServer({ role: args.role, ... })` and pass `advisorNames: args.advisors`:

```javascript
const { server: messengerServer, toolNames } = buildMessengerMcpServer({
  role: args.role,
  msgDir,
  instance: args.instance,
  eventLogRoot: args.eventLogDir || '/logs/events',
  advisorNames: args.advisors,
})
```

In `sdk-mcp-tools.mjs`, update the `buildMessengerMcpServer` signature to accept `advisorNames`:

```javascript
export function buildMessengerMcpServer({ role, msgDir, instance, eventLogRoot, advisorNames }) {
  advisorNames = advisorNames || []
  const tools = []
  // ...
}
```

No tool changes yet — we just plumb the parameter. Validated by subsequent task tests.

- [ ] **Step 5: Run test to verify it passes**

```bash
cd lib/agent-supervisor && node --test args.test.mjs supervisor.test.mjs sdk-mcp-tools.test.mjs
```

Expected: PASS for all.

- [ ] **Step 6: Commit**

```bash
git add lib/agent-supervisor/args.mjs lib/agent-supervisor/supervisor.mjs lib/agent-supervisor/sdk-mcp-tools.mjs lib/agent-supervisor/args.test.mjs
git commit -m "Plumb advisor-name list through supervisor args into MCP builder"
```

---

### Task 8: Worker MCP tools — `notify_coordinator`, `ask_coordinator`, `shutdown_self`

**Files:**
- Modify: `lib/agent-supervisor/sdk-mcp-tools.mjs`
- Test: `lib/agent-supervisor/sdk-mcp-tools.test.mjs`

The simplest role set — worker → coordinator and worker self-shutdown. Advisors come next.

- [ ] **Step 1: Write the failing test**

Append to `sdk-mcp-tools.test.mjs`:

```javascript
import { buildMessengerMcpServer } from './sdk-mcp-tools.mjs'

function findTool(server, name) {
  const full = `mcp__agent-messenger__${name}`
  // createSdkMcpServer stores tools internally; we test via toolNames listing.
  return full
}

test('worker role exposes notify_coordinator, ask_coordinator, shutdown_self', () => {
  const { toolNames } = buildMessengerMcpServer({
    role: 'agent',
    msgDir: '/tmp',
    instance: 'issue-42',
    eventLogRoot: '/tmp',
    advisorNames: [],
  })
  assert.ok(toolNames.includes('mcp__agent-messenger__notify_coordinator'))
  assert.ok(toolNames.includes('mcp__agent-messenger__ask_coordinator'))
  assert.ok(toolNames.includes('mcp__agent-messenger__shutdown_self'))
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd lib/agent-supervisor && node --test sdk-mcp-tools.test.mjs
```

Expected: FAIL (tool names missing).

- [ ] **Step 3: Register the tools**

In `sdk-mcp-tools.mjs`, after the existing `if (role === 'coordinator') { ... }` block, add:

```javascript
if (role === 'agent') {
  tools.push(
    tool(
      'notify_coordinator',
      'Send a status update or completion message to the coordinator. Fire-and-forget — no reply expected. Use this for: "issue-N complete, PR: ...", progress updates, and error reports.',
      {
        message: z.string().describe('The message body. Plain text.'),
      },
      async ({ message }) => {
        const sockPath = sockPathFor(msgDir, '_coordinator')
        const r = await sendAndFetchStatus(sockPath, message)
        if (!r.delivered) {
          return toolResult(buildErrorEnvelope({
            recipient: '_coordinator',
            sockPath,
            reason: r.error,
          }))
        }
        return toolResult(buildDeliveredEnvelope({
          recipient: '_coordinator',
          status: r.status,
          expectedReplyWindow: 'none (fire-and-forget notification)',
          guidance: 'Continue your current task. The coordinator will see this as a new user turn on its side.',
        }))
      },
    ),

    tool(
      'ask_coordinator',
      'Ask the coordinator a question. Expect a reply arriving later as a new user turn on your session (not as a tool result). Use when your task scope is ambiguous and only the coordinator can resolve it.',
      {
        question: z.string().describe('The question to ask. Be specific; include context the coordinator may not remember.'),
      },
      async ({ question }) => {
        const sockPath = sockPathFor(msgDir, '_coordinator')
        const r = await sendAndFetchStatus(sockPath, `[question from ${instance}] ${question}`)
        if (!r.delivered) {
          return toolResult(buildErrorEnvelope({
            recipient: '_coordinator',
            sockPath,
            reason: r.error,
          }))
        }
        return toolResult(buildDeliveredEnvelope({
          recipient: '_coordinator',
          status: r.status,
          expectedReplyWindow: '~1-5 min (coordinator reply time)',
          guidance: 'Continue work on parts of your task that do not depend on the answer. When the reply arrives as a new user message, integrate it and proceed.',
        }))
      },
    ),

    tool(
      'shutdown_self',
      'Close your own supervisor cleanly. Call this ONLY after notify_coordinator with your final completion message (task done, PR opened, etc.). Your container stays up; the coordinator can reclaim it.',
      {},
      async () => {
        const sockPath = sockPathFor(msgDir, instance)
        const reply = await trySocketRequest(sockPath, { cmd: 'shutdown_self' })
        if (!reply.ok) {
          return toolResult({ ok: false, error: reply.error || 'shutdown failed' })
        }
        return toolResult({ ok: true, message: 'Shutdown requested. Supervisor will exit shortly.' })
      },
    ),
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
cd lib/agent-supervisor && node --test sdk-mcp-tools.test.mjs
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/agent-supervisor/sdk-mcp-tools.mjs lib/agent-supervisor/sdk-mcp-tools.test.mjs
git commit -m "Add worker MCP tools: notify_coordinator, ask_coordinator, shutdown_self"
```

---

### Task 9: Advisor-targeting tools for workers and coordinators

**Files:**
- Modify: `lib/agent-supervisor/sdk-mcp-tools.mjs`
- Test: `lib/agent-supervisor/sdk-mcp-tools.test.mjs`

Adds `handshake_advisor`, `ask_advisor`, and `send_to_advisor`. The `name` parameter is validated against the `advisorNames` enum.

- [ ] **Step 1: Write the failing test**

Append to `sdk-mcp-tools.test.mjs`:

```javascript
test('worker role exposes handshake_advisor and ask_advisor when advisors are configured', () => {
  const { toolNames } = buildMessengerMcpServer({
    role: 'agent',
    msgDir: '/tmp',
    instance: 'issue-42',
    eventLogRoot: '/tmp',
    advisorNames: ['decision-maker'],
  })
  assert.ok(toolNames.includes('mcp__agent-messenger__handshake_advisor'))
  assert.ok(toolNames.includes('mcp__agent-messenger__ask_advisor'))
})

test('coordinator role exposes ask_advisor and send_to_advisor when advisors are configured', () => {
  const { toolNames } = buildMessengerMcpServer({
    role: 'coordinator',
    msgDir: '/tmp',
    eventLogRoot: '/tmp',
    advisorNames: ['decision-maker'],
  })
  assert.ok(toolNames.includes('mcp__agent-messenger__ask_advisor'))
  assert.ok(toolNames.includes('mcp__agent-messenger__send_to_advisor'))
})

test('advisor tools are omitted when no advisors configured', () => {
  const { toolNames } = buildMessengerMcpServer({
    role: 'agent',
    msgDir: '/tmp',
    instance: 'issue-42',
    eventLogRoot: '/tmp',
    advisorNames: [],
  })
  assert.ok(!toolNames.includes('mcp__agent-messenger__handshake_advisor'))
  assert.ok(!toolNames.includes('mcp__agent-messenger__ask_advisor'))
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd lib/agent-supervisor && node --test sdk-mcp-tools.test.mjs
```

Expected: FAIL.

- [ ] **Step 3: Register the advisor-targeting tools**

In `sdk-mcp-tools.mjs`, add a helper first (near the top of `buildMessengerMcpServer`):

```javascript
function advisorNameSchema(names) {
  if (names.length === 0) {
    // empty enum is invalid; callers guard against this by checking length first
    return z.string()
  }
  return z.enum(names)
}

const HANDSHAKE_GUIDANCE =
  'No reply expected. The advisor will do a shallow research pass so it is warm for later questions.'
const QUESTION_GUIDANCE =
  'Continue work on parts of your task that do not depend on the answer. When the reply arrives as a new user message, integrate it and proceed.'
```

Then, inside `buildMessengerMcpServer`, gate on `advisorNames.length > 0`:

```javascript
const hasAdvisors = advisorNames.length > 0

if (role === 'agent' && hasAdvisors) {
  tools.push(
    tool(
      'handshake_advisor',
      'Tell an advisor what you are about to work on, so it warms its research context. Fire-and-forget — the advisor will not reply to you. Call once per task, near the start.',
      {
        name: advisorNameSchema(advisorNames),
        summary: z.string().describe('One-line summary of what you are working on (e.g. "issue #42: add retry logic to webhook handler")'),
        hints: z.array(z.string()).optional().describe('Up to ~5 file or module hints that might be relevant'),
      },
      async ({ name, summary, hints }) => {
        const sockPath = sockPathFor(msgDir, name)
        const body = `[handshake from ${instance}] ${summary}\nHints: ${(hints || []).join(', ') || '(none)'}`
        const r = await sendAndFetchStatus(sockPath, body)
        if (!r.delivered) {
          return toolResult(buildErrorEnvelope({ recipient: name, sockPath, reason: r.error }))
        }
        return toolResult(buildDeliveredEnvelope({
          recipient: name,
          status: r.status,
          expectedReplyWindow: 'none (handshake)',
          guidance: HANDSHAKE_GUIDANCE,
        }))
      },
    ),
  )
}

if ((role === 'agent' || role === 'coordinator') && hasAdvisors) {
  tools.push(
    tool(
      'ask_advisor',
      'Ask an advisor a question. Reply arrives later as a new user turn on your session, not as a tool result.',
      {
        name: advisorNameSchema(advisorNames),
        question: z.string().describe('The question. Be specific; the advisor only sees what you send.'),
        kind: z.enum(['question', 'followup']).default('question').describe('"question" = first ask; "followup" = tighter question in an ongoing consultation'),
      },
      async ({ name, question, kind }) => {
        const sockPath = sockPathFor(msgDir, name)
        const sender = instance || '_coordinator'
        const body = `[${kind} from ${sender}] ${question}`
        const r = await sendAndFetchStatus(sockPath, body)
        if (!r.delivered) {
          return toolResult(buildErrorEnvelope({ recipient: name, sockPath, reason: r.error }))
        }
        return toolResult(buildDeliveredEnvelope({
          recipient: name,
          status: r.status,
          expectedReplyWindow: kind === 'followup' ? '~1-5 min' : '~2-10 min (complex question)',
          guidance: QUESTION_GUIDANCE,
        }))
      },
    ),
  )
}

if (role === 'coordinator' && hasAdvisors) {
  tools.push(
    tool(
      'send_to_advisor',
      'Send a fire-and-forget note to an advisor (no reply expected). Use for informational updates like "issue-42 has been merged" or "new principle added to principles.md".',
      {
        name: advisorNameSchema(advisorNames),
        message: z.string(),
      },
      async ({ name, message }) => {
        const sockPath = sockPathFor(msgDir, name)
        const r = await sendAndFetchStatus(sockPath, `[note from _coordinator] ${message}`)
        if (!r.delivered) {
          return toolResult(buildErrorEnvelope({ recipient: name, sockPath, reason: r.error }))
        }
        return toolResult(buildDeliveredEnvelope({
          recipient: name,
          status: r.status,
          expectedReplyWindow: 'none (note)',
          guidance: 'No reply expected.',
        }))
      },
    ),
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
cd lib/agent-supervisor && node --test sdk-mcp-tools.test.mjs
```

Expected: PASS (3 new tests).

- [ ] **Step 5: Commit**

```bash
git add lib/agent-supervisor/sdk-mcp-tools.mjs lib/agent-supervisor/sdk-mcp-tools.test.mjs
git commit -m "Add advisor-targeting MCP tools: handshake_advisor, ask_advisor, send_to_advisor"
```

---

### Task 10: Coordinator `send_to_worker` tool

**Files:**
- Modify: `lib/agent-supervisor/sdk-mcp-tools.mjs`
- Test: `lib/agent-supervisor/sdk-mcp-tools.test.mjs`

- [ ] **Step 1: Write the failing test**

Append to `sdk-mcp-tools.test.mjs`:

```javascript
test('coordinator role exposes send_to_worker', () => {
  const { toolNames } = buildMessengerMcpServer({
    role: 'coordinator',
    msgDir: '/tmp',
    eventLogRoot: '/tmp',
    advisorNames: [],
  })
  assert.ok(toolNames.includes('mcp__agent-messenger__send_to_worker'))
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd lib/agent-supervisor && node --test sdk-mcp-tools.test.mjs
```

Expected: FAIL.

- [ ] **Step 3: Register the tool**

In the existing `if (role === 'coordinator') { ... }` block inside `buildMessengerMcpServer`, add a new `tools.push(tool(...))` entry alongside the diagnostic tools:

```javascript
tool(
  'send_to_worker',
  'Send a message to a worker agent. Fire-and-forget but the worker will process it as a new user turn. Use for directives like "rebase onto feature/x and resolve conflicts" or answers to ask_coordinator questions.',
  {
    instance: z.string().describe('Worker instance name (e.g. "issue-42")'),
    message: z.string(),
  },
  async ({ instance: target, message }) => {
    const sockPath = sockPathFor(msgDir, target)
    const r = await sendAndFetchStatus(sockPath, `[directive from _coordinator] ${message}`)
    if (!r.delivered) {
      return toolResult(buildErrorEnvelope({ recipient: target, sockPath, reason: r.error }))
    }
    return toolResult(buildDeliveredEnvelope({
      recipient: target,
      status: r.status,
      expectedReplyWindow: 'worker acts on the message; no automatic reply',
      guidance: 'If you expect the worker to notify you back, wait for a new user turn from that worker.',
    }))
  },
),
```

- [ ] **Step 4: Run test to verify it passes**

```bash
cd lib/agent-supervisor && node --test sdk-mcp-tools.test.mjs
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/agent-supervisor/sdk-mcp-tools.mjs lib/agent-supervisor/sdk-mcp-tools.test.mjs
git commit -m "Add send_to_worker MCP tool for the coordinator"
```

---

### Task 11: Advisor role MCP tools — `reply_to_coordinator`, `reply_to_worker`, `note_to_coordinator`

**Files:**
- Modify: `lib/agent-supervisor/sdk-mcp-tools.mjs`
- Test: `lib/agent-supervisor/sdk-mcp-tools.test.mjs`

Registers the advisor role's tool set. The advisor is identified as `role === 'advisor'` — supervisor.mjs support for that role comes next task.

- [ ] **Step 1: Write the failing test**

Append to `sdk-mcp-tools.test.mjs`:

```javascript
test('advisor role exposes reply and note tools', () => {
  const { toolNames } = buildMessengerMcpServer({
    role: 'advisor',
    msgDir: '/tmp',
    instance: 'decision-maker',
    eventLogRoot: '/tmp',
    advisorNames: ['decision-maker'],
  })
  assert.ok(toolNames.includes('mcp__agent-messenger__reply_to_coordinator'))
  assert.ok(toolNames.includes('mcp__agent-messenger__reply_to_worker'))
  assert.ok(toolNames.includes('mcp__agent-messenger__note_to_coordinator'))
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd lib/agent-supervisor && node --test sdk-mcp-tools.test.mjs
```

Expected: FAIL.

- [ ] **Step 3: Register the advisor role's tools**

In `sdk-mcp-tools.mjs`, add a new block in `buildMessengerMcpServer`:

```javascript
if (role === 'advisor') {
  tools.push(
    tool(
      'reply_to_coordinator',
      'Send your recommendation/answer back to the coordinator. The coordinator will see this as a new user turn. Use this as the final step of a consultation when the coordinator asked you via ask_advisor.',
      {
        message: z.string().describe('Your full reply. Follow the response shape in your system prompt.'),
      },
      async ({ message }) => {
        const sockPath = sockPathFor(msgDir, '_coordinator')
        const r = await sendAndFetchStatus(sockPath, `[advisor ${instance} reply] ${message}`)
        if (!r.delivered) {
          return toolResult(buildErrorEnvelope({
            recipient: '_coordinator',
            sockPath,
            reason: r.error,
          }))
        }
        return toolResult(buildDeliveredEnvelope({
          recipient: '_coordinator',
          status: r.status,
          expectedReplyWindow: 'none (coordinator reads it on its next turn)',
          guidance: 'Consultation delivered.',
        }))
      },
    ),

    tool(
      'reply_to_worker',
      'Send your answer back to a worker that asked you via ask_advisor. The worker will see it as a new user turn mid-task.',
      {
        instance: z.string().describe('Worker instance name (e.g. "issue-42")'),
        message: z.string(),
      },
      async ({ instance: target, message }) => {
        const sockPath = sockPathFor(msgDir, target)
        const r = await sendAndFetchStatus(sockPath, `[advisor ${instance} reply] ${message}`)
        if (!r.delivered) {
          return toolResult(buildErrorEnvelope({ recipient: target, sockPath, reason: r.error }))
        }
        return toolResult(buildDeliveredEnvelope({
          recipient: target,
          status: r.status,
          expectedReplyWindow: 'none',
          guidance: 'Consultation delivered.',
        }))
      },
    ),

    tool(
      'note_to_coordinator',
      'Proactively ping the coordinator with a short note (e.g. during handshake research you found a conflicting prior decision the coordinator should see). Use sparingly — the default on handshakes is silence.',
      {
        message: z.string(),
      },
      async ({ message }) => {
        const sockPath = sockPathFor(msgDir, '_coordinator')
        const r = await sendAndFetchStatus(sockPath, `[advisor ${instance} note] ${message}`)
        if (!r.delivered) {
          return toolResult(buildErrorEnvelope({
            recipient: '_coordinator',
            sockPath,
            reason: r.error,
          }))
        }
        return toolResult(buildDeliveredEnvelope({
          recipient: '_coordinator',
          status: r.status,
          expectedReplyWindow: 'none',
          guidance: 'Note delivered.',
        }))
      },
    ),
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
cd lib/agent-supervisor && node --test sdk-mcp-tools.test.mjs
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/agent-supervisor/sdk-mcp-tools.mjs lib/agent-supervisor/sdk-mcp-tools.test.mjs
git commit -m "Add advisor-role MCP tools: reply_to_coordinator, reply_to_worker, note_to_coordinator"
```

---

### Task 12: Teach supervisor.mjs about `role=advisor`

**Files:**
- Modify: `lib/agent-supervisor/args.mjs`
- Modify: `lib/agent-supervisor/supervisor.mjs`
- Test: `lib/agent-supervisor/args.test.mjs`, `lib/agent-supervisor/supervisor.test.mjs`

Advisor mode is agent-like (socket path = `/logs/messages/<name>/supervisor.sock`) but always multi-turn (like coordinator).

- [ ] **Step 1: Write the failing test**

Append to `lib/agent-supervisor/args.test.mjs`:

```javascript
test('role=advisor is accepted', () => {
  const args = parseArgsForTest([
    'node', 'supervisor.mjs',
    '--role', 'advisor',
    '--instance', 'decision-maker',
    '--prompt-file', '/tmp/p',
  ])
  assert.equal(args.role, 'advisor')
})

test('role=unknown throws', () => {
  assert.throws(() => parseArgsForTest([
    'node', 'supervisor.mjs',
    '--role', 'bogus',
    '--instance', 'x',
    '--prompt-file', '/tmp/p',
  ]))
})
```

And to `supervisor.test.mjs` (test the helper):

```javascript
import { deriveRoleBehavior } from './supervisor.mjs'

test('role=advisor is multi-turn and uses instance subdir', () => {
  const b = deriveRoleBehavior({ role: 'advisor', instance: 'decision-maker' })
  assert.equal(b.isMultiTurn, true)
  assert.equal(b.socketInstance, 'decision-maker')
  assert.equal(b.eventSubdir, 'decision-maker')
})

test('role=coordinator is multi-turn and uses _coordinator subdir', () => {
  const b = deriveRoleBehavior({ role: 'coordinator' })
  assert.equal(b.isMultiTurn, true)
  assert.equal(b.socketInstance, '_coordinator')
  assert.equal(b.eventSubdir, '_coordinator')
})

test('role=agent is one-shot by default', () => {
  const b = deriveRoleBehavior({ role: 'agent', instance: 'issue-42', persistent: false })
  assert.equal(b.isMultiTurn, false)
  assert.equal(b.socketInstance, 'issue-42')
})

test('role=agent with --persistent is multi-turn', () => {
  const b = deriveRoleBehavior({ role: 'agent', instance: 'issue-42', persistent: true })
  assert.equal(b.isMultiTurn, true)
})
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd lib/agent-supervisor && node --test args.test.mjs supervisor.test.mjs
```

Expected: FAIL.

- [ ] **Step 3: Add role validation and helper**

In `args.mjs`, after parsing the role, add:

```javascript
if (!['agent', 'coordinator', 'advisor'].includes(args.role)) {
  throw new Error(`unknown role: ${args.role}`)
}
```

In `supervisor.mjs`, add near the other top-level helpers:

```javascript
/**
 * Derive role-dependent behavior. Pure function for testability.
 */
export function deriveRoleBehavior({ role, instance, persistent }) {
  const isMultiTurn = role === 'coordinator' || role === 'advisor' || persistent === true
  const socketInstance = role === 'coordinator' ? '_coordinator' : instance
  const eventSubdir = role === 'coordinator' ? '_coordinator' : instance
  return { isMultiTurn, socketInstance, eventSubdir }
}
```

And replace the existing computation of `isMultiTurn` and `eventSubdir` in `main()` with calls to this helper. Also update `socketPathFor` to accept a pre-resolved instance:

```javascript
// Before: const isMultiTurn = args.role === 'coordinator' || args.persistent === true
// After:
const behavior = deriveRoleBehavior({
  role: args.role,
  instance: args.instance,
  persistent: args.persistent,
})
const isMultiTurn = behavior.isMultiTurn
const eventSubdir = behavior.eventSubdir
```

And `socketPathFor(args.role, msgDir, args.instance)` becomes:

```javascript
const sockPath = path.join(msgDir, behavior.socketInstance, 'supervisor.sock')
```

(Remove `socketPathFor` or keep it — whichever is less churn. Simpler: keep it but update its internals to use role/instance directly without special-casing.)

- [ ] **Step 4: Also handle the system prompt fallback**

For role=advisor, agents and advisors share the `agent-system-prompt.txt` fallback (advisors always pass `--system-prompt-file` so this is rarely used, but match the pattern). In `main()`:

```javascript
const systemPromptFile =
  args.systemPromptFile ||
  path.join(
    __dirname,
    args.role === 'coordinator' ? 'coordinator-system-prompt.txt' : 'agent-system-prompt.txt',
  )
```

This works unchanged — `args.role === 'coordinator'` is false for advisor, so it falls through to agent. No change needed here.

- [ ] **Step 5: Also handle the status reply role field**

In the idle timer and elsewhere where `args.role` is stringified in log messages, add `advisor` to the label logic if present (grep for `'coordinator' ? 'coordinator' : 'persistent agent'`):

Replace:
```javascript
const label = args.role === 'coordinator' ? 'coordinator' : 'persistent agent'
```

With:
```javascript
const label = args.role === 'coordinator' ? 'coordinator' : args.role === 'advisor' ? 'advisor' : 'persistent agent'
```

Apply in both occurrences inside `main()` (idle timer log line and result-turn log line).

- [ ] **Step 6: Run tests**

```bash
cd lib/agent-supervisor && node --test args.test.mjs supervisor.test.mjs sdk-mcp-tools.test.mjs
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add lib/agent-supervisor/args.mjs lib/agent-supervisor/supervisor.mjs lib/agent-supervisor/args.test.mjs lib/agent-supervisor/supervisor.test.mjs
git commit -m "Teach supervisor.mjs about role=advisor (multi-turn, instance-scoped socket)"
```

---

### Task 13: Add `RoleAdvisor` constant and thread advisor options through `LaunchParams`

**Files:**
- Modify: `internal/supervisor/dispatch.go`
- Modify: `internal/supervisor/launch.go`
- Test: `internal/supervisor/launch_test.go`

The current `buildSupervisorArgs` pulls `cfg.Claude.Model` and `cfg.Claude.Effort`. Advisors need per-advisor overrides.

- [ ] **Step 1: Write the failing test**

Append to `internal/supervisor/launch_test.go`:

```go
func TestBuildSupervisorArgsAdvisor(t *testing.T) {
	cfg := &config.Config{}
	cfg.Claude.Model = "default-model"
	cfg.Claude.Effort = "high"

	params := LaunchParams{
		Name:          "decision-maker",
		Role:          RoleAdvisor,
		PromptFile:    "/tmp/p",
		StderrLog:     "/tmp/err",
		ModelOverride: "claude-opus-4-7",
		EffortOverride: "max",
		AdvisorNames:  []string{"decision-maker", "critic"},
	}
	args := buildSupervisorArgs(params, cfg)

	if !containsArg(args, "--role", "advisor") {
		t.Errorf("expected --role advisor; got %v", args)
	}
	if !containsArg(args, "--model", "claude-opus-4-7") {
		t.Errorf("expected --model claude-opus-4-7 (override); got %v", args)
	}
	if !containsArg(args, "--effort", "max") {
		t.Errorf("expected --effort max (override); got %v", args)
	}
	if !containsArg(args, "--advisors", "decision-maker,critic") {
		t.Errorf("expected --advisors decision-maker,critic; got %v", args)
	}
	if !containsArg(args, "--instance", "decision-maker") {
		t.Errorf("expected --instance decision-maker; got %v", args)
	}
}

// Helper: walks pairs and returns true if [flag, value] appears consecutively.
func containsArg(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/supervisor/ -run TestBuildSupervisorArgsAdvisor -v
```

Expected: FAIL — fields undefined.

- [ ] **Step 3: Add `RoleAdvisor` constant**

In `internal/supervisor/dispatch.go`:

```go
const (
	RoleAgent       = "agent"
	RoleCoordinator = "coordinator"
	RoleAdvisor     = "advisor"
)
```

- [ ] **Step 4: Add override fields to `LaunchParams`**

In `internal/supervisor/launch.go`, extend `LaunchParams`:

```go
type LaunchParams struct {
	// ... existing fields ...

	// ModelOverride, if non-empty, takes precedence over cfg.Claude.Model
	// on the supervisor command line. Used by advisors to pin their model
	// independently of the rest of the cspace session.
	ModelOverride string

	// EffortOverride, if non-empty, takes precedence over cfg.Claude.Effort.
	EffortOverride string

	// AdvisorNames is the list of configured advisor names to pass to the
	// supervisor for MCP tool enum population.
	AdvisorNames []string
}
```

- [ ] **Step 5: Update `buildSupervisorArgs`**

In `buildSupervisorArgs`, replace the `cfg.Claude.Model` / `cfg.Claude.Effort` block with:

```go
	model := params.ModelOverride
	if model == "" {
		model = cfg.Claude.Model
	}
	if model != "" {
		args = append(args, "--model", model)
	}

	effort := params.EffortOverride
	if effort == "" {
		effort = cfg.Claude.Effort
	}
	if effort == "" {
		effort = "max"
	}
	args = append(args, "--effort", effort)

	if len(params.AdvisorNames) > 0 {
		args = append(args, "--advisors", strings.Join(params.AdvisorNames, ","))
	}
```

Also update the `--instance` emit condition:

```go
	if params.Role == RoleAgent || params.Role == RoleAdvisor {
		args = append(args, "--instance", params.Name)
	}
```

(Before: `if params.Role == RoleAgent { ... }`.)

- [ ] **Step 6: Run test to verify it passes**

```bash
go test ./internal/supervisor/ -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/supervisor/dispatch.go internal/supervisor/launch.go internal/supervisor/launch_test.go
git commit -m "Add RoleAdvisor and thread model/effort/advisorNames through LaunchParams"
```

---

### Task 14: Create `internal/advisor` package with `Launch`, `IsAlive`, `Teardown`

**Files:**
- Create: `internal/advisor/advisor.go`
- Create: `internal/advisor/advisor_test.go`

This package centralizes advisor lifecycle. `cspace coordinate` and `cspace advisor` both call into it.

- [ ] **Step 1: Write the failing test**

Create `internal/advisor/advisor_test.go`:

```go
package advisor

import (
	"testing"

	"github.com/elliottregan/cspace/internal/config"
)

func TestBuildLaunchParams(t *testing.T) {
	cfg := &config.Config{
		ProjectRoot: "/tmp/proj",
		AssetsDir:   "/opt/assets",
		Advisors: map[string]config.AdvisorConfig{
			"decision-maker": {
				Model:      "claude-opus-4-7",
				Effort:     "max",
				BaseBranch: "main",
			},
		},
	}

	params, err := BuildLaunchParams(cfg, "decision-maker")
	if err != nil {
		t.Fatalf("BuildLaunchParams: %v", err)
	}
	if params.ModelOverride != "claude-opus-4-7" {
		t.Errorf("Model: %s", params.ModelOverride)
	}
	if params.EffortOverride != "max" {
		t.Errorf("Effort: %s", params.EffortOverride)
	}
	if len(params.AdvisorNames) != 1 || params.AdvisorNames[0] != "decision-maker" {
		t.Errorf("AdvisorNames: %v", params.AdvisorNames)
	}
	if params.Name != "decision-maker" {
		t.Errorf("Name: %s", params.Name)
	}
}

func TestBuildLaunchParamsUnknownAdvisor(t *testing.T) {
	cfg := &config.Config{Advisors: map[string]config.AdvisorConfig{}}
	_, err := BuildLaunchParams(cfg, "missing")
	if err == nil {
		t.Fatal("expected error for unknown advisor")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/advisor/... -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 3: Create `internal/advisor/advisor.go`**

```go
// Package advisor manages the lifecycle of long-running advisor agents.
// See docs/superpowers/specs/2026-04-18-advisor-agents-design.md.
package advisor

import (
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"time"

	"github.com/elliottregan/cspace/internal/config"
	"github.com/elliottregan/cspace/internal/supervisor"
)

// BuildLaunchParams assembles the LaunchParams for a configured advisor.
// Returns an error if the advisor is not in cfg.Advisors.
func BuildLaunchParams(cfg *config.Config, name string) (supervisor.LaunchParams, error) {
	spec, ok := cfg.Advisors[name]
	if !ok {
		return supervisor.LaunchParams{}, fmt.Errorf("advisor %q not configured", name)
	}

	return supervisor.LaunchParams{
		Name:             name,
		Role:             supervisor.RoleAdvisor,
		ModelOverride:    spec.Model,
		EffortOverride:   spec.Effort,
		AdvisorNames:     sortedAdvisorNames(cfg),
		SystemPromptFile: "", // caller stages the system-prompt file; see Launch
		Persistent:       true,
	}, nil
}

// sortedAdvisorNames returns the configured advisor names in sorted order
// so the --advisors CLI flag is deterministic.
func sortedAdvisorNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Advisors))
	for n := range cfg.Advisors {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// IsAlive returns true when the advisor's supervisor socket answers a
// status request. Reused from the coordinator's liveness probe pattern.
func IsAlive(cfg *config.Config, name string) bool {
	logsPath := supervisor.ResolveLogsVolumePath(cfg)
	if logsPath == "" {
		return false
	}
	sockPath := filepath.Join(logsPath, name, "supervisor.sock")
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	req, _ := json.Marshal(map[string]string{"cmd": "status"})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return false
	}
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return false
	}
	var reply struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(buf[:n], &reply); err != nil {
		return false
	}
	return reply.OK
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/advisor/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/advisor/advisor.go internal/advisor/advisor_test.go
git commit -m "Add internal/advisor package with BuildLaunchParams and IsAlive"
```

---

### Task 15: Implement `advisor.Launch` and `advisor.Teardown`

**Files:**
- Modify: `internal/advisor/advisor.go`

These are thin wrappers over existing `provision.Run`, `supervisor.LaunchSupervisor` (detached), and container stop. No new unit tests — launching a real container is integration-level, covered in Task 21.

- [ ] **Step 1: Add `Launch`**

Append to `internal/advisor/advisor.go`:

```go
import (
	// existing imports...
	"os"
	"github.com/elliottregan/cspace/internal/instance"
	"github.com/elliottregan/cspace/internal/provision"
)

// Launch brings the named advisor up if not already alive. It:
//   1. provisions the container if missing
//   2. checks out the configured baseBranch
//   3. stages the system prompt and bootstrap prompt
//   4. launches the supervisor detached (role=advisor, persistent)
//
// If the advisor is already alive, Launch is a no-op (returns nil).
func Launch(cfg *config.Config, name string) error {
	spec, ok := cfg.Advisors[name]
	if !ok {
		return fmt.Errorf("advisor %q not configured", name)
	}

	if IsAlive(cfg, name) {
		return nil
	}

	if _, err := provision.Run(provision.Params{Name: name, Cfg: cfg}); err != nil {
		return fmt.Errorf("provisioning advisor %s: %w", name, err)
	}

	composeName := cfg.ComposeName(name)
	_ = instance.SkipOnboarding(composeName)

	// Re-copy host .env so the advisor inherits GH_TOKEN, etc. (matches coordinator behavior).
	envFile := filepath.Join(cfg.ProjectRoot, ".env")
	if _, err := os.Stat(envFile); err == nil {
		_ = instance.DcCp(composeName, envFile, "/workspace/.env")
		_, _ = instance.DcExecRoot(composeName, "chown", "dev:dev", "/workspace/.env")
	}

	// Check out the configured baseBranch (default main) inside the advisor's workspace.
	// The advisor can switch branches itself later if a consultation requires it.
	baseBranch := spec.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}
	// git fetch is best-effort; container may be offline. checkout must succeed.
	_, _ = instance.DcExec(composeName, "git", "-C", "/workspace", "fetch", "origin")
	if _, err := instance.DcExec(composeName, "git", "-C", "/workspace", "checkout", baseBranch); err != nil {
		return fmt.Errorf("checking out advisor baseBranch %s: %w", baseBranch, err)
	}

	// Stage the system prompt (ResolveAdvisor handles override/fallback).
	systemPromptHost := cfg.ResolveAdvisor(name)
	if systemPromptHost == "" {
		return fmt.Errorf("no system prompt resolved for advisor %s", name)
	}
	const containerSystemPromptPath = "/tmp/advisor-system-prompt.txt"
	if err := supervisor.StagePromptFile(composeName, systemPromptHost, containerSystemPromptPath); err != nil {
		return fmt.Errorf("staging advisor system prompt: %w", err)
	}

	// Render and stage the bootstrap prompt.
	bootstrap := renderBootstrapPrompt(name)
	const containerBootstrapPath = "/tmp/advisor-bootstrap.txt"
	if err := supervisor.StagePromptText(composeName, bootstrap, containerBootstrapPath); err != nil {
		return fmt.Errorf("staging advisor bootstrap prompt: %w", err)
	}

	params, err := BuildLaunchParams(cfg, name)
	if err != nil {
		return err
	}
	params.PromptFile = containerBootstrapPath
	params.StderrLog = supervisor.ContainerAgentStderrLog
	params.SystemPromptFile = containerSystemPromptPath

	// Detached launch — the coordinator does not block on advisor stdout.
	return supervisor.RelaunchDetached(params, cfg, 0)
}

func renderBootstrapPrompt(name string) string {
	return fmt.Sprintf(`You are the %s advisor. Your role is defined in your system prompt
(already applied to this session).

Project principles, direction, and decisions live in the cspace-context
server — call read_context at the start of each consultation for current
values.

You will receive messages via the agent-messenger MCP tools. Reply via
reply_to_coordinator / reply_to_worker. See your system prompt for
response format and quality bar.

Do a light read of read_context(["direction","principles","roadmap"])
now so you have baseline context. Then wait for messages.`, name)
}

// Teardown shuts down the advisor's supervisor and stops its container.
// Session state is lost.
func Teardown(cfg *config.Config, name string) error {
	if _, ok := cfg.Advisors[name]; !ok {
		return fmt.Errorf("advisor %q not configured", name)
	}
	composeName := cfg.ComposeName(name)

	// Best-effort interrupt the supervisor (closes prompt queue cleanly).
	_ = supervisor.Dispatch(composeName, "interrupt", name)

	// Stop the container and remove volumes — same path as `cspace down`.
	if err := compose.Run(name, cfg, "down", "--volumes"); err != nil {
		return fmt.Errorf("stopping advisor container: %w", err)
	}
	return nil
}
```

Add `"github.com/elliottregan/cspace/internal/compose"` to the import block.

- [ ] **Step 2: Verify the package still builds**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 3: Run the advisor tests**

```bash
go test ./internal/advisor/... -v
```

Expected: PASS (still, existing tests unaffected).

- [ ] **Step 4: Commit**

```bash
git add internal/advisor/advisor.go
git commit -m "Add advisor.Launch and advisor.Teardown"
```

---

### Task 16: `cspace coordinate` launches configured advisors at startup

**Files:**
- Modify: `internal/cli/coordinate.go`

On startup, iterate `cfg.Advisors`, call `advisor.Launch(cfg, name)` (which is a no-op if already alive), then proceed to launch the coordinator. Errors in advisor launch are logged but don't abort the coordinator.

- [ ] **Step 1: Add import and launch loop**

In `internal/cli/coordinate.go`:

Add to imports:

```go
"github.com/elliottregan/cspace/internal/advisor"
```

In `runCoordinateWithArgs`, after the `coordinatorIsAlive()` check but before `provision.Run(provision.Params{Name: name, Cfg: cfg})`, insert:

```go
	// Bring up all configured advisors. A fresh advisor is provisioned and
	// launched persistent; an already-alive advisor is reused so its session
	// continuity is preserved across cspace coordinate calls.
	for adName := range cfg.Advisors {
		if err := advisor.Launch(cfg, adName); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: advisor %s failed to launch: %v\n", adName, err)
		} else if advisor.IsAlive(cfg, adName) {
			fmt.Fprintf(os.Stderr, "Advisor %s ready.\n", adName)
		}
	}
```

- [ ] **Step 2: Also pass `AdvisorNames` to the coordinator's own LaunchParams**

In the final `supervisor.LaunchSupervisor(supervisor.LaunchParams{...}, cfg)` call in `runCoordinateWithArgs`, add the `AdvisorNames` field:

```go
return supervisor.LaunchSupervisor(supervisor.LaunchParams{
    Name:             name,
    Role:             supervisor.RoleCoordinator,
    PromptFile:       supervisor.ContainerCoordPromptPath,
    StderrLog:        supervisor.ContainerCoordStderrLog,
    SystemPromptFile: containerSystemPrompt,
    AdvisorNames:     advisor.SortedAdvisorNames(cfg),
}, cfg)
```

To use `SortedAdvisorNames`, export it from the advisor package. In `internal/advisor/advisor.go`, rename `sortedAdvisorNames` to `SortedAdvisorNames` and update the call inside `BuildLaunchParams`.

- [ ] **Step 3: Build and test**

```bash
make vet && go build ./... && go test ./...
```

Expected: all pass.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/coordinate.go internal/advisor/advisor.go
git commit -m "cspace coordinate launches configured advisors at startup"
```

---

### Task 17: Render advisor roster into coordinator's first user turn

**Files:**
- Modify: `internal/cli/coordinate.go`

The coordinator playbook references an advisor roster placeholder; render it from config.

- [ ] **Step 1: Add the roster rendering**

In `internal/cli/coordinate.go`, after reading `playbookBytes` and before constructing `fullPrompt`, add a roster string:

```go
// Render the advisor roster into the coordinator's first user turn so it
// knows what names are valid for ask_advisor/send_to_advisor.
var rosterBuilder strings.Builder
if len(cfg.Advisors) > 0 {
    rosterBuilder.WriteString("\n\n## Advisor roster (available via ask_advisor / send_to_advisor)\n\n")
    for _, adName := range advisor.SortedAdvisorNames(cfg) {
        spec := cfg.Advisors[adName]
        model := spec.Model
        if model == "" {
            model = "(account default)"
        }
        effort := spec.Effort
        if effort == "" {
            effort = "(default)"
        }
        fmt.Fprintf(&rosterBuilder, "- **%s** — model=%s, effort=%s\n", adName, model, effort)
    }
}
```

Add `"strings"` to the imports if not already present.

Then update the `fullPrompt` assembly for the default (non-systemPromptFile) branch:

```go
fullPrompt = string(playbookBytes) + rosterBuilder.String() + "\n\nUSER INSTRUCTIONS:\n\n" + userBody
```

(Before: just playbookBytes + userBody.)

- [ ] **Step 2: Verify build**

```bash
make vet && go build ./...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/cli/coordinate.go
git commit -m "Render advisor roster into coordinator's first user turn"
```

---

### Task 18: Default coordinator to Sonnet with effort=high

**Files:**
- Modify: `internal/cli/coordinate.go`

When the user has not set `claude.model` in config, default the coordinator to Sonnet. Per-advisor models are independent.

- [ ] **Step 1: Add defaults**

In `runCoordinateWithArgs`, before the final `supervisor.LaunchSupervisor` call, insert:

```go
	// Coordinator defaults to Sonnet — deep reasoning is delegated to
	// advisors (Opus). User can override via claude.model in .cspace.json.
	coordModel := cfg.Claude.Model
	if coordModel == "" {
		coordModel = "claude-sonnet-4-6"
	}
	coordEffort := cfg.Claude.Effort
	if coordEffort == "" {
		coordEffort = "high"
	}
```

Update the `LaunchSupervisor` call to pass overrides:

```go
return supervisor.LaunchSupervisor(supervisor.LaunchParams{
    Name:             name,
    Role:             supervisor.RoleCoordinator,
    PromptFile:       supervisor.ContainerCoordPromptPath,
    StderrLog:        supervisor.ContainerCoordStderrLog,
    SystemPromptFile: containerSystemPrompt,
    AdvisorNames:     advisor.SortedAdvisorNames(cfg),
    ModelOverride:    coordModel,
    EffortOverride:   coordEffort,
}, cfg)
```

- [ ] **Step 2: Build**

```bash
make vet && go build ./...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/cli/coordinate.go
git commit -m "Default coordinator to Sonnet with effort=high"
```

---

### Task 19: `cspace advisor` subcommand group

**Files:**
- Create: `internal/cli/advisor.go`
- Modify: `internal/cli/root.go`

- [ ] **Step 1: Create the subcommand group**

Create `internal/cli/advisor.go`:

```go
package cli

import (
	"fmt"

	"github.com/elliottregan/cspace/internal/advisor"
	"github.com/spf13/cobra"
)

func newAdvisorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "advisor",
		Short:   "Manage long-running advisor agents (decision-maker, etc.)",
		GroupID: "agents",
	}
	cmd.AddCommand(newAdvisorListCmd())
	cmd.AddCommand(newAdvisorDownCmd())
	cmd.AddCommand(newAdvisorRestartCmd())
	return cmd
}

func newAdvisorListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured advisors and their liveness",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(cfg.Advisors) == 0 {
				fmt.Println("No advisors configured. See `advisors` in defaults.json.")
				return nil
			}
			for _, name := range advisor.SortedAdvisorNames(cfg) {
				spec := cfg.Advisors[name]
				alive := advisor.IsAlive(cfg, name)
				status := "stopped"
				if alive {
					status = "alive"
				}
				fmt.Printf("%-20s model=%s effort=%s %s\n",
					name,
					fallback(spec.Model, "(default)"),
					fallback(spec.Effort, "(default)"),
					status,
				)
			}
			return nil
		},
	}
}

func newAdvisorDownCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "down [name]",
		Short: "Stop an advisor (or all with --all)",
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			if all {
				for _, name := range advisor.SortedAdvisorNames(cfg) {
					if err := advisor.Teardown(cfg, name); err != nil {
						fmt.Printf("WARN: %s: %v\n", name, err)
					} else {
						fmt.Printf("%s stopped.\n", name)
					}
				}
				return nil
			}
			if len(args) != 1 {
				return fmt.Errorf("usage: cspace advisor down <name> | --all")
			}
			return advisor.Teardown(cfg, args[0])
		},
	}
	cmd.Flags().Bool("all", false, "Tear down every configured advisor")
	return cmd
}

func newAdvisorRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart <name>",
		Short: "Tear down and relaunch an advisor with a fresh session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := advisor.Teardown(cfg, args[0]); err != nil {
				fmt.Printf("WARN during teardown: %v\n", err)
			}
			return advisor.Launch(cfg, args[0])
		},
	}
}

func fallback(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
```

- [ ] **Step 2: Register the command**

In `internal/cli/root.go`, find the spot where other commands are added (search for `rootCmd.AddCommand(newCoordinateCmd())` or similar) and add:

```go
rootCmd.AddCommand(newAdvisorCmd())
```

- [ ] **Step 3: Verify the help output**

```bash
make build && ./bin/cspace-go advisor --help
```

Expected: shows `list`, `down`, `restart` subcommands.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/advisor.go internal/cli/root.go
git commit -m "Add cspace advisor list|down|restart subcommand group"
```

---

### Task 20: Update coordinator playbook — Phase 0.5, triggers, worker `--persistent`

**Files:**
- Modify: `lib/agents/coordinator.md`

Pure markdown edit. No TDD — the supervisor integration tests (Task 21) exercise the resulting flow.

- [ ] **Step 1: Insert Phase 0.5**

In `lib/agents/coordinator.md`, find `## Phase 1 — Setup` and insert immediately before it:

```markdown
## Phase 0.5 — Advisors

You have a bench of advisors available — long-running specialists you can consult. The advisor roster is rendered into this prompt by `cspace coordinate` at launch (see above).

**Consulting an advisor:** use the `ask_advisor(name, question, kind)` MCP tool. The reply arrives later as a new user turn on your session, not as a tool result. This means:

- Your current turn ends after the tool returns (the tool only delivers the question).
- You keep your full session context; the reply, when it arrives, has access to everything you were thinking about.
- You can process other events (worker completions, directives) while waiting.

**Consult the decision-maker when:**
- Picking a base branch or merge ordering when dependencies are ambiguous.
- Dispatching an implementer for an underspecified issue (multiple valid interpretations).
- A worker reports a design-level blocker (e.g. "need to introduce abstraction X — proceed?").
- A PR's diff doesn't cleanly match its acceptance criteria.
- A prior decision or finding seems to conflict with the current work.
- Any choice you judge architecturally significant.

**Do NOT consult for:**
- Routine orchestration (which port, which file, which commit message).
- Choices the existing playbook already prescribes.
- Questions that don't touch principles.md or direction.md.

If you're unsure whether a choice qualifies, ask. A tight question is cheaper than the wrong call.

For fire-and-forget notes (e.g. "issue-42 merged, decisions log updated"), use `send_to_advisor(name, message)`.

---
```

- [ ] **Step 2: Update the Phase 2 launch command to pass `--persistent`**

In the same file, find the `cspace up` command inside Phase 2:

```bash
cspace up issue-$N --base $BASE --prompt-file /tmp/implementer-$N.txt
```

Replace with:

```bash
cspace up issue-$N --base $BASE --prompt-file /tmp/implementer-$N.txt --persistent
```

- [ ] **Step 3: Update the "direct cspace send" passages to prefer MCP tools**

Find `cspace send _coordinator` / `cspace send issue-<N>` references throughout the playbook. Add a short note at the end of the "Rules" section at the bottom:

```markdown
- **Prefer MCP tools over `cspace send`** for inter-agent messaging: `send_to_worker`, `ask_advisor`, `send_to_advisor` give you typed recipient names and structured return values. `cspace send` still works as the underlying transport and is fine for humans/scripts/debugging.
```

- [ ] **Step 4: Sync embedded assets**

```bash
make sync-embedded
```

Expected: embedded copy refreshed.

- [ ] **Step 5: Commit**

```bash
git add lib/agents/coordinator.md internal/assets/embedded
git commit -m "Coordinator playbook: add Phase 0.5 advisors, --persistent on worker launch, prefer MCP tools"
```

---

### Task 21: Update implementer playbook — handshake, ask_advisor, shutdown_self

**Files:**
- Modify: `lib/agents/implementer.md`

- [ ] **Step 1: Handshake at Setup**

Open `lib/agents/implementer.md` and find the Setup phase. Add at the end of that phase:

```markdown
### Advisor handshake

After reading your task prompt and initial `read_context`, call:

```
handshake_advisor(
  name="decision-maker",
  summary="<one-line summary of your task>",
  hints=["path/to/file1", "module/name", "..."]
)
```

This warms the decision-maker's context so it's ready for any consultations later in the task. Do not wait for a reply (the advisor will not reply to a handshake). Continue to Explore.

You may receive mid-task messages from the advisor if it finds something urgent (e.g. a conflicting prior decision). Treat these the way you'd treat a new directive from the coordinator: read, adjust, continue.
```

- [ ] **Step 2: `ask_advisor` in Design/Implement phases**

Find the Design or Implement phase. Add a new subsection:

```markdown
### When to ask the decision-maker

If you hit an architectural choice you can't confidently resolve against `principles.md` and prior decisions in the context server, call:

```
ask_advisor(
  name="decision-maker",
  question="<specific question with context>",
  kind="question"
)
```

The reply arrives later as a new user turn on your session, not as a tool result. Continue working on parts of the task that don't depend on the answer. When the reply lands, integrate it and proceed.

Don't ask for: trivial naming, formatting, or which file to edit. Do ask for: cross-cutting design decisions that affect other agents' work or conflict with existing decisions.
```

- [ ] **Step 3: `shutdown_self` at Ship phase**

Find the Ship phase. After the `notify_coordinator` (or `cspace send _coordinator`) step at the end, add:

```markdown
### Release your supervisor

After the coordinator-notification message has been delivered, call:

```
shutdown_self()
```

This closes your supervisor cleanly so the coordinator isn't left tracking an idle persistent agent. Your container stays up; the coordinator can reclaim it with `cspace down` or reuse it with `cspace up`.
```

- [ ] **Step 4: Replace `cspace send _coordinator` references with `notify_coordinator`**

Search the file for `cspace send _coordinator`. For each occurrence, add a note right after the bash snippet:

```markdown
(Equivalent MCP tool: `notify_coordinator(message="...")`. Preferred for structured return values.)
```

Don't delete the bash form — both are valid.

- [ ] **Step 5: Sync embedded assets**

```bash
make sync-embedded
```

- [ ] **Step 6: Commit**

```bash
git add lib/agents/implementer.md internal/assets/embedded
git commit -m "Implementer playbook: advisor handshake, ask_advisor mid-task, shutdown_self at Ship"
```

---

### Task 22: Add "Advisors" section to CLAUDE.md

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Insert the section**

In `CLAUDE.md`, after the "Project Context" section (or next to it — anywhere logical), add:

```markdown
## Advisors

Advisors are long-running specialist agents consulted alongside the coordinator. Each runs in its own cspace container as `role=advisor` — persistent, session-continuous across `cspace coordinate` invocations. They are declared in `.cspace.json` under `advisors` (see defaults for the decision-maker).

- **Role prompts** live at `lib/advisors/<name>.md` (shipped) or `.cspace/advisors/<name>.md` (per-project override).
- **Opinions** come from `.cspace/context/principles.md` (human-owned per the context-server spec). Populate it with the project's architectural preferences; the decision-maker reads it on each consultation.
- **Lifecycle:** `cspace coordinate` auto-launches configured advisors. `cspace advisor list|down|restart` manages them explicitly. Advisors persist across coordinator sessions so their SDK sessions accumulate project context.
- **Communication:** coordinators and workers consult via the `ask_advisor` MCP tool on the agent-messenger server. Replies arrive as new user turns, not tool results.

See `docs/superpowers/specs/2026-04-18-advisor-agents-design.md` for the full design.
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "Document advisors in CLAUDE.md"
```

---

### Task 23: End-to-end smoke test — supervisor advisor round-trip

**Files:**
- Create: `lib/agent-supervisor/supervisor.integration.test.mjs`

Integration-style test using real Unix sockets and a simplified advisor stub. Skipped unless `RUN_INTEGRATION=1` so CI doesn't need Docker.

- [ ] **Step 1: Create the integration test**

Create `lib/agent-supervisor/supervisor.integration.test.mjs`:

```javascript
import { test, before, after } from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import fs from 'node:fs'
import path from 'node:path'
import os from 'node:os'
import {
  sendAndFetchStatus,
} from './sdk-mcp-tools.mjs'

const enabled = process.env.RUN_INTEGRATION === '1'

function makeFakeSupervisor(sockPath, statusReply) {
  const received = []
  const srv = net.createServer((conn) => {
    let buf = ''
    conn.on('data', (chunk) => {
      buf += chunk.toString('utf-8')
      while (true) {
        const idx = buf.indexOf('\n')
        if (idx < 0) break
        const line = buf.slice(0, idx)
        buf = buf.slice(idx + 1)
        try {
          const req = JSON.parse(line)
          received.push(req)
          if (req.cmd === 'send_user_message') {
            conn.write(JSON.stringify({ ok: true }) + '\n')
          } else if (req.cmd === 'status') {
            conn.write(JSON.stringify({ ok: true, status: statusReply }) + '\n')
          }
        } catch (e) {
          conn.write(JSON.stringify({ ok: false, error: e.message }) + '\n')
        }
      }
    })
  })
  return new Promise((resolve) =>
    srv.listen(sockPath, () => resolve({ srv, received })),
  )
}

test('send_to_advisor round-trip delivers and reports status', { skip: !enabled }, async () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'advisor-int-'))
  const sockPath = path.join(tmp, 'supervisor.sock')
  const { srv, received } = await makeFakeSupervisor(sockPath, {
    role: 'advisor',
    instance: 'decision-maker',
    sessionId: 'session-1',
    turns: 3,
    lastActivityMs: 0,
    queue_depth: 0,
    git_branch: 'main',
  })
  try {
    const r = await sendAndFetchStatus(sockPath, 'test message')
    assert.equal(r.delivered, true)
    assert.equal(r.status.instance, 'decision-maker')
    assert.equal(r.status.git_branch, 'main')
    assert.deepEqual(
      received.map((req) => req.cmd),
      ['send_user_message', 'status'],
    )
  } finally {
    srv.close()
    fs.rmSync(tmp, { recursive: true, force: true })
  }
})
```

- [ ] **Step 2: Run the test (with flag)**

```bash
cd lib/agent-supervisor && RUN_INTEGRATION=1 node --test supervisor.integration.test.mjs
```

Expected: PASS.

- [ ] **Step 3: Verify it's skipped by default**

```bash
cd lib/agent-supervisor && node --test supervisor.integration.test.mjs
```

Expected: 1 test, 0 failures, 1 skipped (or `ok 1 - SKIP`).

- [ ] **Step 4: Commit**

```bash
git add lib/agent-supervisor/supervisor.integration.test.mjs
git commit -m "Add advisor round-trip integration test (opt-in via RUN_INTEGRATION=1)"
```

---

### Task 24: Final verification

**Files:** none (verification only)

- [ ] **Step 1: Full test suite**

```bash
make vet && make test
```

Expected: all green.

- [ ] **Step 2: Full build**

```bash
make build
```

Expected: `./bin/cspace-go` produced.

- [ ] **Step 3: Smoke-test the CLI**

```bash
./bin/cspace-go advisor list
./bin/cspace-go advisor --help
./bin/cspace-go coordinate --help
```

Expected: commands list advisors (decision-maker, stopped), help text renders cleanly.

- [ ] **Step 4: Commit any stray fixups discovered**

If verification surfaced issues, fix them inline and commit with a fixup message.

---

## Summary of commits

Expected commit history after executing this plan:

1. Add AdvisorConfig struct and Advisors map to Config
2. Add ResolveAdvisor for advisor system-prompt path resolution
3. Ship default decision-maker system prompt and defaults.json advisors block
4. Extend supervisor status socket with queue_depth and cached git_branch
5. Extract handleSupervisorRequest and add shutdown_self command alias
6. Add MCP-tool helpers for socket send+status and envelope building
7. Plumb advisor-name list through supervisor args into MCP builder
8. Add worker MCP tools: notify_coordinator, ask_coordinator, shutdown_self
9. Add advisor-targeting MCP tools: handshake_advisor, ask_advisor, send_to_advisor
10. Add send_to_worker MCP tool for the coordinator
11. Add advisor-role MCP tools: reply_to_coordinator, reply_to_worker, note_to_coordinator
12. Teach supervisor.mjs about role=advisor (multi-turn, instance-scoped socket)
13. Add RoleAdvisor and thread model/effort/advisorNames through LaunchParams
14. Add internal/advisor package with BuildLaunchParams and IsAlive
15. Add advisor.Launch and advisor.Teardown
16. cspace coordinate launches configured advisors at startup
17. Render advisor roster into coordinator's first user turn
18. Default coordinator to Sonnet with effort=high
19. Add cspace advisor list|down|restart subcommand group
20. Coordinator playbook: Phase 0.5 advisors, --persistent on worker launch, prefer MCP tools
21. Implementer playbook: advisor handshake, ask_advisor mid-task, shutdown_self at Ship
22. Document advisors in CLAUDE.md
23. Add advisor round-trip integration test (opt-in via RUN_INTEGRATION=1)
