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
import { shim } from './stream-shim.mjs'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

// --- Arg parsing (minimal, no deps) ---

function parseArgs(argv) {
  const args = { role: 'agent', idleTimeoutMs: 10 * 60 * 1000 }
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
  --no-effort-max               Skip passing --effort max to the CLI`)
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

  // Inbox watcher — agent role only. A cross-container coordinator cannot
  // reach this supervisor's Unix socket, so it drops directives/responses
  // into /logs/messages/{instance}/inbox/ and we pick them up here and
  // inject them as new user turns. Responses (type=response) are consumed
  // by the in-process ask_orchestrator tool via its own fs.watch — don't
  // inject those as user turns or the agent sees the reply twice.
  let inboxWatcher = null
  if (args.role === 'agent' && args.instance) {
    const inboxDir = path.join(msgDir, args.instance, 'inbox')
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
        // Only inject directives as user turns. Responses unblock
        // ask_orchestrator via its own watcher; notifications etc. are
        // not for the agent to consume here.
        if (msg && msg.type === 'directive' && typeof msg.content === 'string') {
          promptQueue.push(makeUserMessage(`[orchestrator directive] ${msg.content}`))
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

      if (msg && msg.type === 'result') {
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

  cleanup()
  process.exit(exitCode)
}

main().catch((e) => {
  console.error(`[supervisor] fatal: ${e.stack || e.message}`)
  process.exit(1)
})
