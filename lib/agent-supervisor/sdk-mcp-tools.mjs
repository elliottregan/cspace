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

import { randomUUID } from 'node:crypto'
import fs from 'node:fs'
import path from 'node:path'
import { createSdkMcpServer, tool } from '@anthropic-ai/claude-agent-sdk'
import { z } from 'zod'

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

/**
 * Build the in-process MCP server with the toolset for the given role.
 *
 * @param {object} config
 * @param {'agent'|'coordinator'} config.role
 * @param {string} config.msgDir   usually /logs/messages
 * @param {string} [config.instance]  required for agent role
 * @returns {{ server: object, toolNames: string[] }}
 */
export function buildMessengerMcpServer({ role, msgDir, instance }) {
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
          const inbox = path.join(msgDir, targetInstance, 'inbox')
          writeMessage(inbox, {
            id: makeId(),
            timestamp: new Date().toISOString(),
            from: 'coordinator',
            type: 'response',
            in_reply_to: question_id,
            content: answer,
          })
          return {
            content: [
              {
                type: 'text',
                text: `Response sent to ${targetInstance}.`,
              },
            ],
          }
        },
      ),

      tool(
        'send_directive',
        'Send a proactive directive to an agent. The agent will see this as an injected user turn (via the supervisor socket if running) or on its next check_messages call.',
        {
          instance: z.string().describe('The agent instance name'),
          message: z.string().describe('The directive message'),
        },
        async ({ instance: targetInstance, message }) => {
          const inbox = path.join(msgDir, targetInstance, 'inbox')
          writeMessage(inbox, {
            id: makeId(),
            timestamp: new Date().toISOString(),
            from: 'coordinator',
            type: 'directive',
            content: message,
          })
          return {
            content: [{ type: 'text', text: `Directive sent to ${targetInstance}.` }],
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
