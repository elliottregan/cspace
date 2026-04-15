/**
 * In-process MCP tools for the agent-supervisor.
 *
 * Uses the same filesystem message schema as the legacy agent-messenger-mcp
 * so in-flight messages survive the cutover:
 *   /logs/messages/{instance}/outbox/msg-*.json  — from agent to coordinator
 *   /logs/messages/{instance}/inbox/msg-*.json   — from coordinator to agent
 *
 * Agent role tools:
 *   ask_orchestrator    blocks until a matching response lands in the inbox
 *                       (fs.watch, not a 2-second poll — this is the main
 *                       latency win for cross-container question/answer)
 *   notify_orchestrator fire-and-forget status update
 *
 * Coordinator role tools:
 *   list_agent_questions scans all instance outboxes for unanswered questions
 *   respond_to_agent     writes a response into the target instance's inbox
 *   send_directive       writes a proactive directive into an inbox
 *
 * The supervisor itself delivers coordinator→agent messages via Unix socket
 * when available; these MCP tools are the cross-container fallback and the
 * agent-initiated path.
 */

import { execFile } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import fs from 'node:fs'
import net from 'node:net'
import path from 'node:path'
import { createSdkMcpServer, tool } from '@anthropic-ai/claude-agent-sdk'
import { z } from 'zod'
import {
  filterStreamForRead,
  findLatestSessionFile,
  readAllEnvelopes,
  readTailEnvelopes,
} from './event-log.mjs'

const ASK_TIMEOUT_MS = 10 * 60 * 1000

function ensureDir(dir) {
  fs.mkdirSync(dir, { recursive: true })
}

function makeId() {
  return `msg-${Date.now()}-${randomUUID().slice(0, 8)}`
}

/** Atomic write: temp file then rename. */
function writeMessage(dir, message) {
  ensureDir(dir)
  const filename = `${message.id}.json`
  const tmpPath = path.join(dir, `.${filename}.tmp`)
  const finalPath = path.join(dir, filename)
  fs.writeFileSync(tmpPath, JSON.stringify(message, null, 2))
  fs.renameSync(tmpPath, finalPath)
  return finalPath
}

/**
 * Write a message file as a dotfile (audit-only record). The inbox
 * watcher filters out files starting with '.', so this won't trigger
 * delivery — it exists purely for the audit trail and future UI.
 */
function writeAuditMessage(dir, message) {
  ensureDir(dir)
  const filename = `.${message.id}.json`
  const tmpPath = path.join(dir, `.${filename}.tmp`)
  const finalPath = path.join(dir, filename)
  fs.writeFileSync(tmpPath, JSON.stringify(message, null, 2))
  fs.renameSync(tmpPath, finalPath)
  return finalPath
}

function readMessages(dir) {
  if (!fs.existsSync(dir)) return []
  return fs
    .readdirSync(dir)
    .filter((f) => f.endsWith('.json') && !f.startsWith('.'))
    .map((f) => {
      try {
        return JSON.parse(fs.readFileSync(path.join(dir, f), 'utf-8'))
      } catch {
        return null
      }
    })
    .filter(Boolean)
}

function listInstances(msgDir) {
  if (!fs.existsSync(msgDir)) return []
  return fs.readdirSync(msgDir).filter((d) => fs.statSync(path.join(msgDir, d)).isDirectory())
}

function isAnswered(msgDir, instance, questionId) {
  const inbox = path.join(msgDir, instance, 'inbox')
  return readMessages(inbox).some((m) => m.in_reply_to === questionId && m.type === 'response')
}

/**
 * Wait for a response with in_reply_to === questionId to land in inboxDir.
 * Uses fs.watch for event-driven delivery; falls back to a slow safety poll
 * in case the watcher misses an event on exotic filesystems.
 */
function waitForResponse(inboxDir, questionId, timeoutMs) {
  return new Promise((resolve) => {
    ensureDir(inboxDir)
    let settled = false
    const finish = (value) => {
      if (settled) return
      settled = true
      clearInterval(pollTimer)
      clearTimeout(deadline)
      try {
        watcher.close()
      } catch {}
      resolve(value)
    }

    const check = () => {
      const messages = readMessages(inboxDir)
      const match = messages.find((m) => m.in_reply_to === questionId && m.type === 'response')
      if (match) finish(match)
    }

    const watcher = fs.watch(inboxDir, () => check())
    // Safety poll every 5s in case fs.watch misses something on a shared volume.
    const pollTimer = setInterval(check, 5000)
    const deadline = setTimeout(() => finish(null), timeoutMs)
    // Initial check in case the reply was already there.
    check()
  })
}

// --- Helpers for coordinator diagnostic tools ---

/**
 * Send a request to a supervisor's Unix socket and return the reply.
 * Used for both status probes and live message injection. Mirrors the
 * pattern in cli.mjs but inlined to avoid a shared-module dependency.
 */
function trySocketRequest(sockPath, request, timeoutMs = 2000) {
  return new Promise((resolve) => {
    if (!fs.existsSync(sockPath)) {
      resolve({ ok: false, error: 'socket not present' })
      return
    }
    const conn = net.createConnection(sockPath)
    let settled = false
    const finish = (value) => {
      if (settled) return
      settled = true
      try {
        conn.end()
      } catch {}
      clearTimeout(timer)
      resolve(value)
    }
    const timer = setTimeout(
      () => finish({ ok: false, error: 'timeout' }),
      timeoutMs,
    )
    let buf = ''
    conn.on('connect', () =>
      conn.write(JSON.stringify(request) + '\n'),
    )
    conn.on('data', (chunk) => {
      buf += chunk.toString('utf-8')
      const idx = buf.indexOf('\n')
      if (idx >= 0) {
        try {
          finish(JSON.parse(buf.slice(0, idx)))
        } catch (e) {
          finish({ ok: false, error: `parse error: ${e.message}` })
        }
      }
    })
    conn.on('error', (e) =>
      finish({ ok: false, error: e.message }),
    )
    conn.on('end', () =>
      finish({ ok: false, error: 'connection closed' }),
    )
  })
}

function sockPathFor(msgDir, instance) {
  return path.join(msgDir, instance, 'supervisor.sock')
}

/** Find the newest session-*.ndjson file for an instance. */
/**
 * Scan envelopes (in order) for the most recent tool_use whose paired
 * tool_result hasn't arrived yet. Stops at the first `result` envelope
 * from the end (clean turn boundary). Returns { tool, tool_id, started_at,
 * age_ms } or null if all tool calls are paired.
 */
function findPendingToolCall(envelopes) {
  // Walk from end to find the last `result` envelope (turn boundary).
  let startIdx = 0
  for (let i = envelopes.length - 1; i >= 0; i--) {
    if (envelopes[i].sdk?.type === 'result') {
      startIdx = i + 1
      break
    }
  }
  // Within this active segment, track unmatched tool_use IDs.
  const pending = new Map() // tool_use_id → { tool, started_at }
  for (let i = startIdx; i < envelopes.length; i++) {
    const env = envelopes[i]
    const content = env.sdk?.message?.content
    if (!Array.isArray(content)) continue
    for (const block of content) {
      if (block.type === 'tool_use' && block.id) {
        pending.set(block.id, { tool: block.name, started_at: env.ts })
      } else if (block.type === 'tool_result' && block.tool_use_id) {
        pending.delete(block.tool_use_id)
      }
    }
  }
  if (pending.size === 0) return null
  // Return the most recently issued unmatched tool call.
  const entries = [...pending.entries()]
  const [toolId, info] = entries[entries.length - 1]
  const startedAt = info.started_at ? new Date(info.started_at).getTime() : Date.now()
  return {
    tool: info.tool,
    tool_id: toolId,
    started_at: info.started_at,
    age_ms: Date.now() - startedAt,
  }
}

/** Promise wrapper around child_process.execFile. */
function execFilePromise(cmd, args, options) {
  return new Promise((resolve, reject) => {
    execFile(cmd, args, options, (err, stdout, stderr) => {
      if (err) {
        err.stderr = stderr
        reject(err)
      } else {
        resolve(stdout.toString().trim())
      }
    })
  })
}

/**
 * Build the in-process MCP server with the toolset for the given role.
 *
 * @param {object} config
 * @param {'agent'|'coordinator'} config.role
 * @param {string} config.msgDir        usually /logs/messages
 * @param {string} [config.instance]    required for agent role
 * @param {string} [config.eventLogRoot] usually /logs/events — used by
 *   coordinator diagnostic tools to find agent event log files
 * @returns {{ server: object, toolNames: string[] }}
 */
export function buildMessengerMcpServer({ role, msgDir, instance, eventLogRoot }) {
  const tools = []

  if (role === 'agent') {
    if (!instance) {
      throw new Error('agent role requires instance name')
    }
    const inboxDir = path.join(msgDir, instance, 'inbox')
    const outboxDir = path.join(msgDir, instance, 'outbox')

    tools.push(
      tool(
        'ask_orchestrator',
        'Ask the orchestrator a blocking question. Blocks up to 10 minutes waiting for a reply, returns the answer directly. Use for genuinely ambiguous decisions with significant trade-offs.',
        {
          question: z.string().describe('The question to ask'),
          context: z
            .string()
            .optional()
            .describe('What you have tried, options you see, trade-offs'),
          urgency: z
            .enum(['blocking', 'informational'])
            .default('blocking')
            .describe('blocking = you cannot proceed without an answer'),
        },
        async ({ question, context, urgency }) => {
          ensureDir(inboxDir)
          ensureDir(outboxDir)
          const msg = {
            id: makeId(),
            timestamp: new Date().toISOString(),
            from: instance,
            type: 'question',
            content: question,
            ...(context && { context }),
            urgency: urgency || 'blocking',
          }
          writeMessage(outboxDir, msg)
          const response = await waitForResponse(inboxDir, msg.id, ASK_TIMEOUT_MS)
          if (response) {
            return { content: [{ type: 'text', text: response.content }] }
          }
          return {
            content: [
              {
                type: 'text',
                text: 'TIMEOUT: No response after 10 minutes. Proceed with your best judgment and note any assumptions.',
              },
            ],
          }
        },
      ),

      tool(
        'notify_orchestrator',
        'Send a non-blocking status update to the orchestrator. Use for milestones (branch created, PR opened, tests passing).',
        {
          message: z.string().describe('The status update'),
          type: z.enum(['progress', 'warning', 'milestone']).default('progress'),
        },
        async ({ message, type }) => {
          ensureDir(outboxDir)
          writeMessage(outboxDir, {
            id: makeId(),
            timestamp: new Date().toISOString(),
            from: instance,
            type: 'notification',
            content: message,
            notificationType: type || 'progress',
          })
          return { content: [{ type: 'text', text: 'Notification sent.' }] }
        },
      ),
    )
  }

  if (role === 'coordinator') {
    tools.push(
      tool(
        'list_agent_questions',
        'List pending (unanswered) questions from all agents, or from a specific instance. Optionally includes notifications.',
        {
          instance: z.string().optional().describe('Filter to a specific agent instance name'),
          include_notifications: z
            .boolean()
            .default(false)
            .describe('Also include progress/warning/milestone notifications'),
        },
        async ({ instance: filterInstance, include_notifications }) => {
          const instances = filterInstance ? [filterInstance] : listInstances(msgDir)
          const results = []
          for (const inst of instances) {
            const outbox = path.join(msgDir, inst, 'outbox')
            for (const msg of readMessages(outbox)) {
              if (msg.type === 'question' && !isAnswered(msgDir, inst, msg.id)) {
                results.push({
                  instance: inst,
                  id: msg.id,
                  timestamp: msg.timestamp,
                  type: 'question',
                  urgency: msg.urgency || 'blocking',
                  content: msg.content,
                  ...(msg.context && { context: msg.context }),
                })
              } else if (include_notifications && msg.type === 'notification') {
                results.push({
                  instance: inst,
                  id: msg.id,
                  timestamp: msg.timestamp,
                  type: msg.notificationType || 'progress',
                  content: msg.content,
                })
              }
            }
          }
          if (results.length === 0) {
            return { content: [{ type: 'text', text: 'No pending questions.' }] }
          }
          results.sort((a, b) => a.timestamp.localeCompare(b.timestamp))
          const formatted = results
            .map((r) => {
              const header = `[${r.instance}] ${
                r.type === 'question' ? `QUESTION (${r.urgency})` : r.type.toUpperCase()
              } — ${r.timestamp}`
              const ctx = r.context ? `\nContext: ${r.context}` : ''
              const idLine = r.type === 'question' ? `\nID: ${r.id} (use this to respond)` : ''
              return `${header}\n${r.content}${ctx}${idLine}`
            })
            .join('\n\n---\n\n')
          return { content: [{ type: 'text', text: formatted }] }
        },
      ),

      tool(
        'respond_to_agent',
        'Respond to a pending agent question. The agent is blocked on this — it will receive your answer and continue.',
        {
          instance: z.string().describe('The agent instance name'),
          question_id: z.string().describe('The message ID of the question'),
          answer: z.string().describe('Your response'),
        },
        async ({ instance: targetInstance, question_id, answer }) => {
          const outbox = path.join(msgDir, targetInstance, 'outbox')
          const question = readMessages(outbox).find(
            (m) => m.id === question_id && m.type === 'question',
          )
          if (!question) {
            return {
              content: [
                {
                  type: 'text',
                  text: `ERROR: Question ${question_id} not found in ${targetInstance}'s outbox.`,
                },
              ],
            }
          }
          if (isAnswered(msgDir, targetInstance, question_id)) {
            return {
              content: [
                { type: 'text', text: `Question ${question_id} has already been answered.` },
              ],
            }
          }
          // Always write the file first — it unblocks the agent's
          // ask_orchestrator tool which is fs.watch-blocked on the inbox
          // waiting for a response with matching in_reply_to. The inbox
          // watcher intentionally skips type=response so there's no
          // double-delivery risk.
          const inbox = path.join(msgDir, targetInstance, 'inbox')
          writeMessage(inbox, {
            id: makeId(),
            timestamp: new Date().toISOString(),
            from: 'coordinator',
            type: 'response',
            in_reply_to: question_id,
            content: answer,
          })

          // Best-effort: also push a user turn via socket so the agent
          // sees the reply as in-conversation context immediately.
          const sockPath = sockPathFor(msgDir, targetInstance)
          const socketReply = await trySocketRequest(sockPath, {
            cmd: 'respond_to_question',
            questionId: question_id,
            text: answer,
          })
          const via = socketReply.ok ? 'file + socket' : 'file only'
          return {
            content: [
              {
                type: 'text',
                text: `Response sent to ${targetInstance} (${via}).`,
              },
            ],
          }
        },
      ),

      tool(
        'send_directive',
        'Send a proactive directive to an agent. Delivered via the live supervisor socket for sub-millisecond injection; also written to disk as an audit record.',
        {
          instance: z.string().describe('The agent instance name'),
          message: z.string().describe('The directive message'),
        },
        async ({ instance: targetInstance, message }) => {
          const inbox = path.join(msgDir, targetInstance, 'inbox')
          const msgPayload = {
            id: makeId(),
            timestamp: new Date().toISOString(),
            from: 'coordinator',
            type: 'directive',
            content: message,
          }

          // Try live socket first — instant delivery via promptQueue.push
          const sockPath = sockPathFor(msgDir, targetInstance)
          const socketReply = await trySocketRequest(sockPath, {
            cmd: 'send_user_message',
            text: `[orchestrator directive] ${message}`,
            source: 'coordinator',
          })

          if (socketReply.ok) {
            // Socket delivered — write the audit file as a dotfile so
            // the agent's inbox watcher doesn't double-deliver it.
            // (The watcher filters out files starting with '.')
            writeAuditMessage(inbox, msgPayload)
            return {
              content: [
                { type: 'text', text: `Directive sent to ${targetInstance} (socket).` },
              ],
            }
          }

          // Socket unavailable — write as a normal inbox file so the
          // agent's inbox watcher picks it up as a fallback.
          writeMessage(inbox, msgPayload)
          return {
            content: [
              {
                type: 'text',
                text: `Directive sent to ${targetInstance} (file fallback, socket: ${socketReply.error}).`,
              },
            ],
          }
        },
      ),

      // --- Diagnostic + recovery tools ---

      tool(
        'agent_health',
        'Check an agent\'s liveness, last activity, and whether it is stuck mid-tool-call. Returns a combined snapshot so you can detect stuck agents in one round-trip.',
        {
          instance: z.string().describe('The agent instance name'),
        },
        async ({ instance: targetInstance }) => {
          // 1. Socket probe — sub-ms liveness check
          const sockPath = sockPathFor(msgDir, targetInstance)
          const statusReply = await trySocketRequest(sockPath, { cmd: 'status' })
          const socketResult = statusReply.ok && statusReply.status
            ? { alive: true, ...statusReply.status }
            : { alive: false, reason: statusReply.error || 'not running' }

          // 2. Event log analysis — pending tool call + latest event ts
          const sessionFile = findLatestSessionFile(eventLogRoot, targetInstance)
          let pendingToolCall = null
          let latestEventTs = null
          if (sessionFile) {
            const envelopes = readTailEnvelopes(sessionFile, 500)
            if (envelopes.length > 0) {
              latestEventTs = envelopes[envelopes.length - 1].ts || null
              pendingToolCall = findPendingToolCall(envelopes)
            }
          }

          const result = {
            alive: socketResult.alive,
            ...(socketResult.alive
              ? {
                  sessionId: socketResult.sessionId || null,
                  turns: socketResult.turns ?? 0,
                  lastActivityMs: socketResult.lastActivityMs ?? null,
                }
              : { reason: socketResult.reason || 'supervisor not running' }),
            latestEventTs,
            pendingToolCall,
            latestSessionFile: sessionFile,
          }
          return {
            content: [{ type: 'text', text: JSON.stringify(result, null, 2) }],
          }
        },
      ),

      tool(
        'agent_recent_activity',
        'Return the last N event-log envelopes from an agent\'s session. Each envelope is { ts, instance, role, sdk } where sdk is the raw SDK message (assistant turns, tool_use, tool_result, result, etc.). Use this to inspect what an agent has been doing recently.',
        {
          instance: z.string().describe('The agent instance name'),
          count: z
            .number()
            .int()
            .min(1)
            .max(50)
            .default(10)
            .describe('Number of envelopes to return (max 50)'),
          types: z
            .array(z.string())
            .optional()
            .describe(
              'Optional filter: only return envelopes where sdk.type is in this list (e.g. ["assistant", "result"])',
            ),
        },
        async ({ instance: targetInstance, count, types }) => {
          const sessionFile = findLatestSessionFile(eventLogRoot, targetInstance)
          if (!sessionFile) {
            return {
              content: [
                {
                  type: 'text',
                  text: `No event log found for instance ${targetInstance}.`,
                },
              ],
            }
          }
          let envelopes = readTailEnvelopes(sessionFile, count * 10) // over-read for filtering
          if (types && types.length > 0) {
            envelopes = envelopes.filter((e) => e.sdk && types.includes(e.sdk.type))
          }
          envelopes = envelopes.slice(-count)
          return {
            content: [{ type: 'text', text: JSON.stringify(envelopes, null, 2) }],
          }
        },
      ),

      tool(
        'read_agent_stream',
        'Read the event stream for an agent instance. This is the same data that appears in the agent\'s `cspace up` stdout/stderr — every SDK event (assistant text, tool_use, tool_result, thinking, result). Use when a background `cspace up` BashOutput was lost, for post-mortem debugging of a completed agent, or to build a watchdog that periodically checks what children are doing. For live monitoring during a run, prefer reading the spawning BashOutput directly; this tool is the stream you can always reach. To poll incrementally, pass the previous call\'s `last_ts` back as `since` on the next call.',
        {
          instance: z.string().describe('Agent instance name (e.g. "mercury", "re-priya-sharma")'),
          since: z
            .string()
            .optional()
            .describe(
              'ISO timestamp — return only envelopes with ts > since (oldest-first). Omit to get the tail.',
            ),
          limit: z
            .number()
            .int()
            .min(1)
            .max(500)
            .default(100)
            .describe('Max envelopes to return (default 100, hard cap 500)'),
          types: z
            .array(z.string())
            .optional()
            .describe(
              'Optional allowlist of sdk.type values (e.g. ["assistant", "result"]) — omit for everything',
            ),
        },
        async ({ instance: targetInstance, since, limit, types }) => {
          const sessionFile = findLatestSessionFile(eventLogRoot, targetInstance)
          if (!sessionFile) {
            return {
              content: [
                {
                  type: 'text',
                  text: `No event log found for instance ${targetInstance}. The agent may not have started yet, or CLAUDE_MSG_DIR/event-log-dir is misconfigured.`,
                },
              ],
            }
          }
          const all = readAllEnvelopes(sessionFile)
          const result = filterStreamForRead({ envelopes: all, since, limit, types })
          return {
            content: [
              {
                type: 'text',
                text: JSON.stringify(
                  {
                    ...result,
                    session_file: path.basename(sessionFile),
                  },
                  null,
                  2,
                ),
              },
            ],
          }
        },
      ),

      tool(
        'restart_agent',
        'Restart a stuck agent\'s supervisor process. Sends an interrupt to unwind the current session, waits for clean exit, then launches a fresh supervisor in the same container (workspace preserved). The old session\'s completion notification will arrive as a separate user turn. A new session starts with the original prompt plus a restart marker explaining the reason.',
        {
          instance: z.string().describe('The agent instance name to restart'),
          reason: z
            .string()
            .optional()
            .describe('Why the restart is needed (e.g. "Playwright transport wedged")'),
        },
        async ({ instance: targetInstance, reason }) => {
          const args = ['restart-supervisor', targetInstance]
          if (reason) args.push('--reason', reason)
          try {
            const output = await execFilePromise('cspace', args, {
              cwd: '/workspace',
              timeout: 45_000,
            })
            return {
              content: [
                {
                  type: 'text',
                  text: `Restart initiated for ${targetInstance}.\n${output}`,
                },
              ],
            }
          } catch (e) {
            return {
              content: [
                {
                  type: 'text',
                  text: `Restart failed for ${targetInstance}: ${e.message}\n${e.stderr || ''}`,
                },
              ],
            }
          }
        },
      ),
    )
  }

  const server = createSdkMcpServer({
    name: 'agent-messenger',
    version: '2.0.0',
    tools,
  })

  const toolNames = tools.map((t) => `mcp__agent-messenger__${t.name}`)
  return { server, toolNames }
}
