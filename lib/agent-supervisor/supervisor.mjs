#!/usr/bin/env node
/**
 * agent-supervisor — long-lived wrapper around the Claude Agent SDK's
 * streaming-input mode that can inject user turns mid-session.
 *
 * Replaces direct `claude --print` invocations in `just issue` and
 * `just coordinate`. The supervisor:
 *   1. starts a `query()` with a queue-backed async iterable prompt stream
 *   2. pumps SDKMessage events through stream-shim to stdout (NDJSON) so
 *      the existing stream-status.sh pipeline keeps working
 *   3. listens on a Unix socket (/logs/messages/{instance}/supervisor.sock
 *      for agents, /logs/messages/_coordinator/supervisor.sock for
 *      coordinators) where host-side `just send/respond/interrupt` commands
 *      are delivered as a new user turn (via queue.push) or an SDK interrupt
 *
 * Roles:
 *   agent       — CLAUDE_INSTANCE set, has ask_orchestrator / notify_orchestrator
 *   coordinator — no CLAUDE_INSTANCE, has list_agent_questions / respond_to_agent / send_directive
 *
 * Invocation (from justfile):
 *   node supervisor.mjs --role agent --instance venus --prompt-file /tmp/claude-prompt.txt
 *   node supervisor.mjs --role coordinator --prompt-file /tmp/coordinator-prompt.txt
 */

import fs from 'node:fs'
import net from 'node:net'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { query } from '@anthropic-ai/claude-agent-sdk'
import { buildMessengerMcpServer } from './sdk-mcp-tools.mjs'
import { normalizeSdkMessage, shim } from './stream-shim.mjs'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

// --- Arg parsing (minimal, no deps) ---

function parseArgs(argv) {
  const args = { role: 'agent', idleTimeoutMs: 10 * 60 * 1000, eventLogDir: '/logs/events' }
  for (let i = 2; i < argv.length; i++) {
    const arg = argv[i]
    if (arg === '--role') args.role = argv[++i]
    else if (arg === '--instance') args.instance = argv[++i]
    else if (arg === '--prompt-file') args.promptFile = argv[++i]
    else if (arg === '--model') args.model = argv[++i]
    else if (arg === '--cwd') args.cwd = argv[++i]
    else if (arg === '--system-prompt-file') args.systemPromptFile = argv[++i]
    else if (arg === '--append-system-prompt') args.appendSystemPrompt = argv[++i]
    else if (arg === '--mcp-config') args.mcpConfig = argv[++i]
    else if (arg === '--idle-timeout-ms') args.idleTimeoutMs = Number.parseInt(argv[++i], 10)
    else if (arg === '--no-effort-max') args.noEffortMax = true
    else if (arg === '--event-log-dir') args.eventLogDir = argv[++i]
    else if (arg === '--help' || arg === '-h') {
      console.error(`Usage: supervisor.mjs --role agent|coordinator [--instance NAME] --prompt-file PATH
Options:
  --role <agent|coordinator>    Default: agent
  --instance <name>             Required for agent role
  --prompt-file <path>          Initial user prompt (required)
  --model <id>                  Default: claude-opus-4-6
  --cwd <path>                  Default: /workspace
  --system-prompt-file <path>   Override the role's default system prompt file
  --append-system-prompt <text> Inline string appended to the system prompt
  --mcp-config <path>           Path to an external MCP config (merged with the
                                in-process agent-messenger tools). Same shape as
                                claude's --mcp-config flag: { mcpServers: {...} }
  --idle-timeout-ms <ms>        Interrupt the run if no SDK event is received for
                                this long. Default 600000 (10 min). 0 disables.
  --no-effort-max               Skip passing --effort max to the CLI
  --event-log-dir <path>        Directory root for the structured NDJSON event
                                log. Default /logs/events. Empty string disables.
                                Files land at <dir>/<instance|_coordinator>/
                                session-<ts>-<sid>.ndjson with an envelope of
                                { ts, instance, role, sdk }.`)
      process.exit(0)
    } else {
      console.error(`supervisor: unknown argument ${arg}`)
      process.exit(2)
    }
  }
  return args
}

// --- Queue-backed async iterable for streaming user messages into query() ---

/**
 * A FIFO queue that yields SDKUserMessage objects as an async iterable.
 * The initial prompt is pushed at startup; socket commands push follow-up
 * user turns while the query is running. close() ends the stream.
 */
class PromptQueue {
  constructor() {
    this._queue = []
    this._resolvers = []
    this._closed = false
  }

  push(userMessage) {
    if (this._closed) return
    if (this._resolvers.length > 0) {
      this._resolvers.shift()(userMessage)
    } else {
      this._queue.push(userMessage)
    }
  }

  close() {
    if (this._closed) return
    this._closed = true
    while (this._resolvers.length > 0) {
      this._resolvers.shift()(undefined)
    }
  }

  async *[Symbol.asyncIterator]() {
    while (true) {
      if (this._queue.length > 0) {
        yield this._queue.shift()
        continue
      }
      if (this._closed) return
      const next = await new Promise((resolve) => this._resolvers.push(resolve))
      if (next === undefined) return
      yield next
    }
  }
}

function makeUserMessage(text, sessionId = '') {
  return {
    type: 'user',
    session_id: sessionId,
    parent_tool_use_id: null,
    message: {
      role: 'user',
      content: [{ type: 'text', text }],
    },
  }
}

// --- Completion notifications (agent → coordinator) ---

function formatDurationMs(ms) {
  if (typeof ms !== 'number' || !Number.isFinite(ms) || ms < 0) return '?'
  const total = Math.round(ms / 1000)
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  if (h > 0) return `${h}h ${m}m ${s}s`
  if (m > 0) return `${m}m ${s}s`
  return `${s}s`
}

/**
 * Render a completion message as a structured user turn for injection
 * into the coordinator's promptQueue. Header line is grep-friendly so
 * the coordinator agent can pattern-match across many concurrent kids;
 * fields below are labeled key: value pairs for easy extraction.
 */
function formatCompletionAsUserTurn(msg) {
  const lines = [`[child agent ${msg.instance || '?'} completed]`]
  if (msg.status) lines.push(`status: ${msg.status}`)
  if (typeof msg.exitCode === 'number') lines.push(`exit_code: ${msg.exitCode}`)
  if (msg.sessionId) lines.push(`session: ${msg.sessionId}`)
  if (typeof msg.turns === 'number') lines.push(`turns: ${msg.turns}`)
  if (typeof msg.durationMs === 'number') {
    lines.push(`duration: ${formatDurationMs(msg.durationMs)}`)
  }
  if (typeof msg.costUsd === 'number') lines.push(`cost: $${msg.costUsd.toFixed(4)}`)
  if (msg.eventLogPath) lines.push(`events: ${msg.eventLogPath}`)
  if (msg.transcriptPath) lines.push(`transcript: ${msg.transcriptPath}`)
  if (msg.summary) lines.push('', msg.summary)
  return lines.join('\n')
}

/**
 * Atomically write a completion notification into the coordinator's inbox.
 * Skips if the coordinator dir doesn't exist (no coordinator was ever
 * started in this project, so a write would just leak files). Returns the
 * written path or null. Failures are logged but never thrown — this runs
 * during process exit and must not block shutdown.
 */
function writeCompletionNotification(msgDir, completion) {
  try {
    const coordDir = path.join(msgDir, '_coordinator')
    if (!fs.existsSync(coordDir)) return null
    const inboxDir = path.join(coordDir, 'inbox')
    fs.mkdirSync(inboxDir, { recursive: true })
    const id = `completion-${completion.instance || 'unknown'}-${Date.now()}`
    const filename = `${id}.json`
    const tmpPath = path.join(inboxDir, `.${filename}.tmp`)
    const finalPath = path.join(inboxDir, filename)
    const payload = { id, ...completion }
    fs.writeFileSync(tmpPath, JSON.stringify(payload, null, 2))
    fs.renameSync(tmpPath, finalPath)
    return finalPath
  } catch (e) {
    console.error(`[supervisor] completion notify failed: ${e.message}`)
    return null
  }
}

// --- Socket server for host-side injection ---

function socketPathFor(role, msgDir, instance) {
  if (role === 'coordinator') {
    return path.join(msgDir, '_coordinator', 'supervisor.sock')
  }
  if (!instance) throw new Error('agent role requires instance')
  return path.join(msgDir, instance, 'supervisor.sock')
}

/**
 * Start the control socket. Commands are NDJSON-framed; each connection
 * handles one or more newline-delimited requests and sends JSON replies.
 *
 * Commands:
 *   { cmd: "send_user_message", text, source? }
 *   { cmd: "respond_to_question", questionId, text }
 *   { cmd: "interrupt" }
 *   { cmd: "status" }
 *   { cmd: "shutdown" }
 */
function startSocket({ sockPath, promptQueue, getQuery, getStatus, onShutdown }) {
  fs.mkdirSync(path.dirname(sockPath), { recursive: true })
  try {
    fs.unlinkSync(sockPath)
  } catch {}

  const server = net.createServer((conn) => {
    let buf = ''
    conn.on('data', (chunk) => {
      buf += chunk.toString('utf-8')
      while (true) {
        const idx = buf.indexOf('\n')
        if (idx < 0) break
        const line = buf.slice(0, idx).trim()
        buf = buf.slice(idx + 1)
        if (!line) continue
        let req
        try {
          req = JSON.parse(line)
        } catch (e) {
          conn.write(JSON.stringify({ ok: false, error: `bad json: ${e.message}` }) + '\n')
          continue
        }
        handleRequest(req)
          .then((reply) => conn.write(JSON.stringify(reply) + '\n'))
          .catch((e) => conn.write(JSON.stringify({ ok: false, error: e.message }) + '\n'))
      }
    })
    conn.on('error', () => {})
  })

  async function handleRequest(req) {
    switch (req.cmd) {
      case 'send_user_message': {
        if (typeof req.text !== 'string' || !req.text) {
          return { ok: false, error: 'text required' }
        }
        promptQueue.push(makeUserMessage(req.text))
        return { ok: true }
      }
      case 'respond_to_question': {
        if (typeof req.questionId !== 'string' || typeof req.text !== 'string') {
          return { ok: false, error: 'questionId and text required' }
        }
        const injected = `[coordinator reply to ${req.questionId}]\n${req.text}`
        promptQueue.push(makeUserMessage(injected))
        return { ok: true }
      }
      case 'interrupt': {
        const q = getQuery()
        if (!q) return { ok: false, error: 'query not yet started' }
        try {
          await q.interrupt()
          return { ok: true }
        } catch (e) {
          return { ok: false, error: e.message }
        }
      }
      case 'status':
        return { ok: true, status: getStatus() }
      case 'shutdown':
        onShutdown()
        return { ok: true }
      default:
        return { ok: false, error: `unknown cmd: ${req.cmd}` }
    }
  }

  server.listen(sockPath, () => {
    try {
      fs.chmodSync(sockPath, 0o660)
    } catch {}
  })
  server.on('error', (e) => {
    console.error(`[supervisor] socket error: ${e.message}`)
  })
  return server
}

// --- Main ---

async function main() {
  const args = parseArgs(process.argv)
  if (!args.promptFile) {
    console.error('supervisor: --prompt-file is required')
    process.exit(2)
  }
  if (args.role === 'agent' && !args.instance) {
    console.error('supervisor: --instance is required for agent role')
    process.exit(2)
  }

  const msgDir = process.env.CLAUDE_MSG_DIR || '/logs/messages'
  const model = args.model || 'claude-opus-4-6'
  const cwd = args.cwd || '/workspace'

  // Event log directory — one subdir per instance (or _coordinator), mirroring
  // the socketPathFor convention. Empty-string disables persistence entirely.
  const eventSubdir = args.role === 'coordinator' ? '_coordinator' : args.instance
  const eventLogDir =
    args.eventLogDir === '' || !eventSubdir ? null : path.join(args.eventLogDir, eventSubdir)
  const startTs = new Date().toISOString().replace(/[:.]/g, '-')

  const initialPrompt = fs.readFileSync(args.promptFile, 'utf-8')

  const systemPromptFile =
    args.systemPromptFile ||
    path.join(
      __dirname,
      args.role === 'coordinator' ? 'coordinator-system-prompt.txt' : 'agent-system-prompt.txt',
    )
  let appendSystemPrompt = fs.existsSync(systemPromptFile)
    ? fs.readFileSync(systemPromptFile, 'utf-8')
    : undefined
  if (args.appendSystemPrompt) {
    appendSystemPrompt = appendSystemPrompt
      ? `${appendSystemPrompt}\n\n${args.appendSystemPrompt}`
      : args.appendSystemPrompt
  }

  const { server: messengerServer, toolNames } = buildMessengerMcpServer({
    role: args.role,
    msgDir,
    instance: args.instance,
  })

  // Merge in external MCP servers from a --mcp-config file (same shape
  // claude's --mcp-config uses: { mcpServers: { name: { command, args, env } } }).
  // Used by `just persona` to load playwright + chrome-devtools alongside
  // the in-process agent-messenger tools.
  const mcpServers = { 'agent-messenger': messengerServer }
  const allowedTools = [...toolNames]
  if (args.mcpConfig) {
    const raw = JSON.parse(fs.readFileSync(args.mcpConfig, 'utf-8'))
    for (const [name, cfg] of Object.entries(raw.mcpServers || {})) {
      // The CLI config shape uses { command, args, env } — the SDK stdio
      // MCP shape is the same plus an explicit `type: 'stdio'`.
      mcpServers[name] = { type: 'stdio', ...cfg }
      allowedTools.push(`mcp__${name}__*`)
    }
  }

  const promptQueue = new PromptQueue()
  promptQueue.push(makeUserMessage(initialPrompt))

  // Track state for socket status / shutdown.
  let queryHandle = null
  let sessionId = ''
  let turnCount = 0
  let lastActivity = Date.now()
  let shuttingDown = false
  let idleTimer = null
  let idleInterrupted = false
  // Captured for the completion notification we emit on exit (agent role only).
  const startMs = Date.now()
  let lastResult = null

  // Inbox watcher — runs for both roles, with the inbox dir derived from
  // the role + instance just like socketPathFor.
  //   agent       /logs/messages/{instance}/inbox/    — directives from coordinator
  //   coordinator /logs/messages/_coordinator/inbox/  — directives + completion
  //                                                     notifications from exiting
  //                                                     child agents
  // Responses (type=response) are consumed by the in-process ask_orchestrator
  // tool via its own fs.watch — don't inject those as user turns or the agent
  // sees the reply twice.
  let inboxWatcher = null
  const inboxDir =
    args.role === 'coordinator'
      ? path.join(msgDir, '_coordinator', 'inbox')
      : args.instance
        ? path.join(msgDir, args.instance, 'inbox')
        : null
  if (inboxDir) {
    fs.mkdirSync(inboxDir, { recursive: true })
    const injectedIds = new Set()
    const scan = () => {
      let entries
      try {
        entries = fs.readdirSync(inboxDir)
      } catch {
        return
      }
      for (const f of entries) {
        if (!f.endsWith('.json') || f.startsWith('.')) continue
        if (injectedIds.has(f)) continue
        injectedIds.add(f)
        let msg
        try {
          msg = JSON.parse(fs.readFileSync(path.join(inboxDir, f), 'utf-8'))
        } catch {
          continue
        }
        if (!msg || typeof msg !== 'object') continue

        if (msg.type === 'directive' && typeof msg.content === 'string') {
          promptQueue.push(makeUserMessage(`[orchestrator directive] ${msg.content}`))
        } else if (msg.type === 'completion' && args.role === 'coordinator') {
          // Structured "child agent finished" notification, written by an
          // exiting agent supervisor. Render it as a self-describing user
          // turn so the coordinator agent can react without polling.
          promptQueue.push(makeUserMessage(formatCompletionAsUserTurn(msg)))
        }
      }
    }
    scan() // catch anything already there at startup
    try {
      inboxWatcher = fs.watch(inboxDir, () => scan())
    } catch (e) {
      console.error(`[supervisor] inbox watch failed: ${e.message}`)
    }
  }

  const sockPath = socketPathFor(args.role, msgDir, args.instance)
  const sockServer = startSocket({
    sockPath,
    promptQueue,
    getQuery: () => queryHandle,
    getStatus: () => ({
      role: args.role,
      instance: args.instance || null,
      sessionId,
      turns: turnCount,
      lastActivityMs: Date.now() - lastActivity,
    }),
    onShutdown: () => {
      shuttingDown = true
      promptQueue.close()
    },
  })

  const cleanup = () => {
    try {
      eventStream?.end()
    } catch {}
    try {
      sockServer.close()
    } catch {}
    try {
      fs.unlinkSync(sockPath)
    } catch {}
    try {
      inboxWatcher?.close()
    } catch {}
    if (idleTimer) {
      clearInterval(idleTimer)
      idleTimer = null
    }
  }
  process.on('SIGTERM', () => {
    shuttingDown = true
    promptQueue.close()
  })
  process.on('SIGINT', () => {
    shuttingDown = true
    promptQueue.close()
  })
  process.on('exit', cleanup)

  console.error(
    `[supervisor] role=${args.role} instance=${args.instance || '(none)'} sock=${sockPath}`,
  )

  // Open the event-log write stream before starting the query so the first
  // system/init message lands in the file. Failures to open must NOT abort
  // the run — a partial log is always better than a dead supervisor, and
  // /logs/events may be missing on hosts that haven't remounted the volume.
  // Filename starts as session-<ts>.partial.ndjson and is renamed to include
  // the session_id once the SDK emits its first message. fs.renameSync on an
  // open fd is safe on Linux (inode follows the fd); the devcontainer is
  // Linux-only so this is fine.
  let eventStream = null
  let eventStreamPath = null
  let eventStreamRenamed = false
  if (eventLogDir) {
    try {
      fs.mkdirSync(eventLogDir, { recursive: true })
      eventStreamPath = path.join(eventLogDir, `session-${startTs}.partial.ndjson`)
      eventStream = fs.createWriteStream(eventStreamPath, { flags: 'a' })
      eventStream.on('error', (e) => {
        console.error(`[supervisor] event log write error: ${e.message}`)
        eventStream = null
      })
    } catch (e) {
      console.error(`[supervisor] event log disabled: ${e.message}`)
      eventStream = null
      eventStreamPath = null
    }
  }

  const queryOptions = {
    model,
    cwd,
    permissionMode: 'bypassPermissions',
    mcpServers,
    allowedTools,
    includePartialMessages: false,
  }
  if (appendSystemPrompt) {
    queryOptions.appendSystemPrompt = appendSystemPrompt
  }

  queryHandle = query({
    prompt: promptQueue,
    options: queryOptions,
  })

  // Idle watchdog — if the SDK hasn't emitted an event for args.idleTimeoutMs,
  // the run is probably wedged on an MCP tool call whose transport died
  // (classic symptom: Chromium sidecar crash hangs playwright MCP indefinitely).
  // We call interrupt() on the query, which unwinds the pending tool call
  // and emits a result with subtype=error_during_execution. The outer recipe
  // treats that as a failure and reports a clean "idle timeout" exit.
  if (args.idleTimeoutMs > 0) {
    const checkIntervalMs = Math.min(30_000, Math.max(5_000, Math.floor(args.idleTimeoutMs / 4)))
    idleTimer = setInterval(() => {
      if (shuttingDown || idleInterrupted) return
      const idle = Date.now() - lastActivity
      if (idle < args.idleTimeoutMs) return
      idleInterrupted = true
      console.error(
        `[supervisor] idle for ${Math.round(idle / 1000)}s (threshold ${Math.round(args.idleTimeoutMs / 1000)}s) — interrupting. Likely stuck on an MCP tool call.`,
      )
      queryHandle?.interrupt().catch((e) => {
        console.error(`[supervisor] interrupt failed: ${e.message}`)
      })
    }, checkIntervalMs)
  }

  let exitCode = 0
  try {
    for await (const msg of queryHandle) {
      lastActivity = Date.now()
      if (msg && msg.session_id) sessionId = msg.session_id
      if (msg && msg.type === 'assistant') turnCount += 1

      const line = shim(msg)
      if (line != null) {
        process.stdout.write(line + '\n')
      }

      // Persist a self-describing envelope to the event log. Written
      // separately from stdout so stream-status.sh still sees the bare SDK
      // shape. A future log viewer can ingest /logs/events/**/*.ndjson
      // without having to parse filenames to learn instance/role.
      if (eventStream && msg) {
        const normalized = normalizeSdkMessage(msg)
        if (normalized) {
          try {
            eventStream.write(
              JSON.stringify({
                ts: new Date().toISOString(),
                instance: args.instance || null,
                role: args.role,
                sdk: normalized,
              }) + '\n',
            )
          } catch (e) {
            console.error(`[supervisor] event log write failed: ${e.message}`)
          }
        }
      }

      // On the first message carrying a session_id, rename the .partial
      // file to include it. If the rename fails (e.g. EXDEV, race), keep
      // writing to the partial path — the file is still valid NDJSON.
      if (
        !eventStreamRenamed &&
        msg &&
        msg.session_id &&
        eventStream &&
        eventStreamPath &&
        eventLogDir
      ) {
        const newPath = path.join(eventLogDir, `session-${startTs}-${msg.session_id}.ndjson`)
        try {
          fs.renameSync(eventStreamPath, newPath)
          eventStreamPath = newPath
        } catch (e) {
          console.error(`[supervisor] event log rename failed: ${e.message}`)
        }
        eventStreamRenamed = true
      }

      if (msg && msg.type === 'result') {
        // Remember the most recent result for the exit-time completion
        // notification — carries num_turns, total_cost_usd, duration_ms.
        lastResult = msg
        // The SDK may emit additional messages after result for multi-turn
        // streaming input, so we do NOT break here; instead, a result marks a
        // "ready for next user turn" point. If nothing else is queued and we
        // were started as a one-shot, close the queue so the iterator ends.
        if (msg.subtype !== 'success') {
          exitCode = 1
        }
        // For now we end after the first result (matches `just issue` semantics).
        // Later we can add a --persist flag to keep the queue open for injection.
        promptQueue.close()
      }
    }
  } catch (e) {
    console.error(`[supervisor] query failed: ${e.stack || e.message}`)
    exitCode = 1
  }

  // Notify the coordinator that we're done. Mirrors Claude Code's
  // background-Agent pattern: when a child task finishes, the parent
  // gets an automatic message instead of having to poll. We only emit
  // this for the agent role — coordinators have nobody to report to.
  // Best-effort: any failure here is logged, never thrown.
  if (args.role === 'agent' && args.instance) {
    let status
    if (idleInterrupted) status = 'idle_timeout'
    else if (lastResult?.subtype === 'success') status = 'success'
    else if (lastResult) status = `error:${lastResult.subtype || 'unknown'}`
    else status = 'crashed'

    const completion = {
      type: 'completion',
      timestamp: new Date().toISOString(),
      from: args.instance,
      instance: args.instance,
      role: args.role,
      status,
      exitCode,
      sessionId: sessionId || null,
      turns: typeof lastResult?.num_turns === 'number' ? lastResult.num_turns : turnCount,
      durationMs:
        typeof lastResult?.duration_ms === 'number' ? lastResult.duration_ms : Date.now() - startMs,
      costUsd:
        typeof lastResult?.total_cost_usd === 'number'
          ? lastResult.total_cost_usd
          : typeof lastResult?.cost_usd === 'number'
            ? lastResult.cost_usd
            : null,
      eventLogPath: eventStreamPath,
      transcriptPath: sessionId ? `/logs/${sessionId}.jsonl` : null,
    }
    writeCompletionNotification(msgDir, completion)
  }

  cleanup()
  process.exit(exitCode)
}

main().catch((e) => {
  console.error(`[supervisor] fatal: ${e.stack || e.message}`)
  process.exit(1)
})
