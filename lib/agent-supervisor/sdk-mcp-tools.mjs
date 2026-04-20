/**
 * In-process MCP tools for the agent-supervisor.
 *
 * All inter-agent messaging goes through `cspace send`:
 *   worker → coordinator:  cspace send _coordinator "..."
 *   coordinator → worker:  cspace send <instance> "..."
 *
 * The tools here are coordinator-only diagnostics for inspecting agent
 * state when the live `cspace up` BashOutput is insufficient:
 *   agent_health          socket liveness + event log pending-tool-call check
 *   agent_recent_activity last N event-log envelopes
 *   read_agent_stream     full event stream with incremental polling
 */

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

// --- Helpers ---

/**
 * Send a request to a supervisor's Unix socket and return the reply.
 * Used for status probes and live message injection.
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

/**
 * Scan envelopes (in order) for the most recent tool_use whose paired
 * tool_result hasn't arrived yet. Stops at the first `result` envelope
 * from the end (clean turn boundary). Returns { tool, tool_id, started_at,
 * age_ms } or null if all tool calls are paired.
 */
function findPendingToolCall(envelopes) {
  let startIdx = 0
  for (let i = envelopes.length - 1; i >= 0; i--) {
    if (envelopes[i].sdk?.type === 'result') {
      startIdx = i + 1
      break
    }
  }
  const pending = new Map()
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

// --- MCP server ---

/**
 * Build the in-process MCP server with diagnostic tools for coordinators.
 * Agent role gets no in-process tools — agents communicate via `cspace send`.
 *
 * @param {object} config
 * @param {'agent'|'coordinator'} config.role
 * @param {string} config.msgDir        usually /logs/messages
 * @param {string} [config.instance]    required for agent role
 * @param {string} [config.eventLogRoot] usually /logs/events
 * @returns {{ server: object, toolNames: string[] }}
 */
export function buildMessengerMcpServer({ role, msgDir, instance, eventLogRoot, advisorNames }) {
  advisorNames = advisorNames || []
  const tools = []

  if (role === 'coordinator') {
    tools.push(
      tool(
        'agent_health',
        'Check an agent\'s liveness, last activity, and whether it is stuck mid-tool-call. Returns a combined snapshot so you can detect stuck agents in one round-trip.',
        {
          instance: z.string().describe('The agent instance name'),
        },
        async ({ instance: targetInstance }) => {
          const sockPath = sockPathFor(msgDir, targetInstance)
          const statusReply = await trySocketRequest(sockPath, { cmd: 'status' })
          const socketResult = statusReply.ok && statusReply.status
            ? { alive: true, ...statusReply.status }
            : { alive: false, reason: statusReply.error || 'not running' }

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
          let envelopes = readTailEnvelopes(sessionFile, count * 10)
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
    )
  }

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

  const server = createSdkMcpServer({
    name: 'agent-messenger',
    version: '3.0.0',
    tools,
  })

  const toolNames = tools.map((t) => `mcp__agent-messenger__${t.name}`)
  return { server, toolNames }
}
