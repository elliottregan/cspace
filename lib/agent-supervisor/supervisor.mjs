#!/usr/bin/env node
/**
 * agent-supervisor — long-lived wrapper around the Claude Agent SDK's
 * streaming-input mode that can inject user turns mid-session.
 *
 * Launched by `cspace up`, `cspace coordinate`, and the teleport resume
 * path. The supervisor:
 *   1. starts a `query()` with a queue-backed async iterable prompt stream
 *   2. pumps SDKMessage events through stream-shim to stdout (NDJSON) so
 *      the Go ProcessStream() pipeline keeps working
 *   3. listens on a Unix socket for `cspace send` / `cspace interrupt`
 *      commands — delivered as new user turns (via queue.push) or SDK
 *      interrupts
 *   4. persists all SDK events to an NDJSON event log on disk
 *
 * Roles:
 *   agent       — one-shot: exits after the first result (or multi-turn with --persistent)
 *   coordinator — multi-turn: stays alive between results, waiting for
 *                 worker completions via `cspace send _coordinator`
 *   advisor     — multi-turn persistent: stays alive to handle consultations
 *                 from coordinator and workers; session continuity preserved
 *                 across cspace coordinate invocations
 *
 * Invocation:
 *   node supervisor.mjs --role agent --instance venus --prompt-file /tmp/claude-prompt.txt
 *   node supervisor.mjs --role coordinator --prompt-file /tmp/coordinator-prompt.txt
 *   node supervisor.mjs --role advisor --instance decision-maker --prompt-file /tmp/p --system-prompt-file /tmp/sys
 */

import fs from 'node:fs'
import net from 'node:net'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { query } from '@anthropic-ai/claude-agent-sdk'
import { parseArgsForTest, buildQueryOptions } from './args.mjs'
import { buildMessengerMcpServer } from './sdk-mcp-tools.mjs'
import { normalizeSdkMessage, shim } from './stream-shim.mjs'
import {
  deriveRoleBehavior,
  computeStatusExtras,
  makeUserMessage,
  handleSupervisorRequest,
} from './supervisor-helpers.mjs'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

// Ignore EPIPE on stdout — the downstream pipe may close before the supervisor
// finishes writing. Without this, an unhandled 'error' event crashes the process.
process.stdout.on('error', (err) => {
  if (err.code === 'EPIPE') return
  throw err
})

// --- Arg parsing ---
//
// The pure parser (parseArgsForTest) and queryOptions builder
// (buildQueryOptions) live in ./args.mjs so that `node --test` can
// exercise them without needing node_modules/ — importing this file
// would eagerly load the Claude Agent SDK.
//
// Re-exported here for backward-compatibility with existing callers.
export {
  parseArgsForTest,
  buildQueryOptions,
  deriveRoleBehavior,
  computeStatusExtras,
  makeUserMessage,
  handleSupervisorRequest,
}

function parseArgs(argv) {
  try {
    return parseArgsForTest(argv)
  } catch (e) {
    if (e.message === '__help__') {
      console.error(`Usage: supervisor.mjs --role agent|coordinator [--instance NAME] [--prompt-file PATH | --resume-session ID]
Options:
  --role <agent|coordinator|advisor>  Default: agent
  --instance <name>             Required for agent role
  --prompt-file <path>          Initial user prompt (required unless --resume-session)
  --resume-session <id>         Resume an existing Claude session by id.
                                Skips initial prompt injection. The transcript
                                at ~/.claude/projects/-workspace/<id>.jsonl must
                                already be present on disk before launch.
  --model <id>                  When omitted, the Claude CLI / account default is used.
  --cwd <path>                  Default: /workspace
  --system-prompt-file <path>   Override the role's default system prompt file
  --append-system-prompt <text> Inline string appended to the system prompt
  --mcp-config <path>           Path to an external MCP config (merged with the
                                in-process agent-messenger tools).
  --idle-timeout-ms <ms>        Interrupt if idle this long. Default 600000.
  --effort <level>              Reasoning effort passed to the SDK (low|medium|high|xhigh|max|auto).
                                Overrides the CLAUDE_CODE_EFFORT_LEVEL env var for this query.
  --persistent                  Agent role only: keep the prompt queue open between
                                results so \`cspace send <instance> ...\` can drive
                                multi-turn conversations. Coordinator and advisor are always persistent.
  --event-log-dir <path>        Directory root for NDJSON event log.
                                Default /logs/events. Empty string disables.
  --advisors <csv>              Comma-separated advisor names (populates MCP tool enums).`)
      process.exit(0)
    }
    console.error(`supervisor: ${e.message}`)
    process.exit(2)
  }
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

// --- Socket server for host-side injection ---

function socketPathFor(role, msgDir, instance) {
  if (role === 'coordinator') {
    return path.join(msgDir, '_coordinator', 'supervisor.sock')
  }
  if (!instance) throw new Error('agent role requires instance')
  return path.join(msgDir, instance, 'supervisor.sock')
}

const execFileAsync = promisify(execFile)

/**
 * Start the control socket. Commands are NDJSON-framed; each connection
 * handles one or more newline-delimited requests and sends JSON replies.
 *
 * Commands:
 *   { cmd: "send_user_message", text, source? }
 *   { cmd: "interrupt" }
 *   { cmd: "status" }
 *   { cmd: "shutdown" }
 *   { cmd: "shutdown_self" }
 */
function startSocket({ sockPath, promptQueue, cwd, getQuery, getStatus, onShutdown }) {
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
    return handleSupervisorRequest(req, {
      promptQueue,
      cwd,
      getQuery,
      getStatus,
      onShutdown,
    })
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
  if (!args.promptFile && !args.resumeSession) {
    console.error('supervisor: --prompt-file or --resume-session is required')
    process.exit(2)
  }
  if (args.role === 'agent' && !args.instance) {
    console.error('supervisor: --instance is required for agent role')
    process.exit(2)
  }

  const msgDir = process.env.CLAUDE_MSG_DIR || '/logs/messages'
  const model = args.model
  const cwd = args.cwd || '/workspace'

  // Coordinator is always multi-turn (stays alive for worker completions).
  // Advisor is always multi-turn (stays alive between turns).
  // Agents go multi-turn only when `--persistent` is passed, letting
  // `cspace send <instance> ...` drive follow-up turns on their own
  // instance-scoped socket.
  const behavior = deriveRoleBehavior({
    role: args.role,
    instance: args.instance,
    persistent: args.persistent,
  })
  const isMultiTurn = behavior.isMultiTurn

  // Event log directory — one subdir per instance (or _coordinator), mirroring
  // the socketPathFor convention. Empty-string disables persistence entirely.
  const eventSubdir = behavior.eventSubdir
  const eventLogDir =
    args.eventLogDir === '' || !eventSubdir ? null : path.join(args.eventLogDir, eventSubdir)
  const startTs = new Date().toISOString().replace(/[:.]/g, '-')

  let initialPrompt = null
  if (!args.resumeSession) {
    initialPrompt = fs.readFileSync(args.promptFile, 'utf-8')
  } else {
    console.error(`[supervisor] resuming session ${args.resumeSession} — no initial prompt`)
  }

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
    eventLogRoot: args.eventLogDir || '/logs/events',
    advisorNames: args.advisors,
  })

  // Merge in external MCP servers from a --mcp-config file (same shape
  // claude's --mcp-config uses: { mcpServers: { name: { command, args, env } } }).
  // Used by persona-coordinator flows (and any other extension that
  // wants playwright / chrome-devtools alongside the in-process
  // agent-messenger tools).
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
  if (!args.resumeSession) {
    promptQueue.push(makeUserMessage(initialPrompt))
  }

  // Track state for socket status / shutdown.
  let queryHandle = null
  let sessionId = ''
  let turnCount = 0
  let lastActivity = Date.now()
  let shuttingDown = false
  let idleTimer = null
  let idleInterrupted = false
  let lastResult = null

  const sockPath = path.join(msgDir, behavior.socketInstance, 'supervisor.sock')
  const sockServer = startSocket({
    sockPath,
    promptQueue,
    cwd,
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

  let cleanupDone = false
  const cleanup = () => {
    if (cleanupDone) return
    cleanupDone = true
    try {
      eventStream?.end()
    } catch {}
    try {
      sockServer.close()
    } catch {}
    try {
      fs.unlinkSync(sockPath)
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

  const queryOptions = buildQueryOptions({
    model,
    effort: args.effort,
    cwd,
    mcpServers,
    allowedTools,
    appendSystemPrompt,
    resumeSession: args.resumeSession,
  })

  queryHandle = query({
    prompt: promptQueue,
    options: queryOptions,
  })

  // Idle watchdog — if the SDK hasn't emitted an event for args.idleTimeoutMs,
  // the run is probably wedged on an MCP tool call whose transport died
  // (classic symptom: Chromium sidecar crash hangs playwright MCP indefinitely).
  //
  // For agents: interrupt() unwinds the pending tool call and emits a result
  // with subtype=error_during_execution. Treated as "idle timeout" failure.
  //
  // For multi-turn sessions (coordinator, persistent agent): the idle
  // period happens *between* turns while waiting for external messages.
  // Interrupting a waiting-for-input query is undefined, so close the
  // promptQueue instead — the async iterator ends, the for-await loop
  // exits, and the session shuts down cleanly. Each incoming message
  // resets lastActivity via the new query cycle it triggers.
  //
  // For one-shot agents: idle means stuck mid-tool-call (classic symptom:
  // a Chromium sidecar crash hangs playwright MCP indefinitely), so
  // interrupt() unwinds the pending tool call.
  if (args.idleTimeoutMs > 0) {
    const checkIntervalMs = Math.min(30_000, Math.max(5_000, Math.floor(args.idleTimeoutMs / 4)))
    idleTimer = setInterval(() => {
      if (shuttingDown || idleInterrupted) return
      const idle = Date.now() - lastActivity
      if (idle < args.idleTimeoutMs) return
      idleInterrupted = true
      if (isMultiTurn) {
        const label = args.role === 'coordinator' ? 'coordinator' : args.role === 'advisor' ? 'advisor' : 'persistent agent'
        console.error(
          `[supervisor] ${label} idle for ${Math.round(idle / 1000)}s — no messages received, shutting down`,
        )
        promptQueue.close()
      } else {
        console.error(
          `[supervisor] idle for ${Math.round(idle / 1000)}s (threshold ${Math.round(args.idleTimeoutMs / 1000)}s) — interrupting. Likely stuck on an MCP tool call.`,
        )
        queryHandle?.interrupt().catch((e) => {
          console.error(`[supervisor] interrupt failed: ${e.message}`)
        })
      }
    }, checkIntervalMs)
  }

  let exitCode = 0
  try {
    for await (const msg of queryHandle) {
      lastActivity = Date.now()
      if (msg && msg.session_id) sessionId = msg.session_id
      if (msg && msg.type === 'assistant') {
        turnCount += 1
      }

      const line = shim(msg)
      if (line != null) {
        process.stdout.write(line + '\n')
      }

      // Persist a self-describing envelope to the event log. Written
      // separately from stdout so ProcessStream() still sees the bare SDK
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
        lastResult = msg
        if (msg.subtype !== 'success') {
          exitCode = 1
        }
        if (isMultiTurn) {
          // Coordinator (always) and persistent agents (via --persistent)
          // stay alive between turns so external callers — worker completions
          // on `cspace send _coordinator`, or `cspace send <instance> ...`
          // for persistent agents — can wake the session for the next query
          // cycle. Reset lastActivity so the idle timer counts from now;
          // each incoming message triggers a new turn which resets it again.
          // The session exits when the idle timer fires (no messages for
          // idleTimeoutMs) or on SIGTERM/SIGINT.
          lastActivity = Date.now()
          const label = args.role === 'coordinator' ? 'coordinator' : args.role === 'advisor' ? 'advisor' : 'persistent agent'
          console.error(
            `[supervisor] ${label} turn complete (${turnCount} turns) — waiting for next message`,
          )
        } else {
          // One-shot agent: close the queue so the iterator ends.
          promptQueue.close()
        }
      }
    }
  } catch (e) {
    console.error(`[supervisor] query failed: ${e.stack || e.message}`)
    exitCode = 1
  }

  // Release the socket and event stream. The process.on('exit', cleanup)
  // handler still fires but is a no-op due to the cleanupDone flag.
  cleanup()

  process.exit(exitCode)
}

// Only launch the supervisor when this file is executed directly, not when
// imported by tests. import.meta.main is Node 24+; fall back to argv[1] check.
const isEntryPoint =
  import.meta.main ?? process.argv[1] === fileURLToPath(import.meta.url)
if (isEntryPoint) {
  main().catch((e) => {
    console.error(`[supervisor] fatal: ${e.stack || e.message}`)
    process.exit(1)
  })
}
