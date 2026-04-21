/**
 * Socket-based protocol helpers extracted from sdk-mcp-tools.mjs.
 * These have no SDK dependencies, so they can be imported in tests
 * without requiring node_modules/ to be installed.
 */

import fs from 'node:fs'
import net from 'node:net'
import path from 'node:path'

/**
 * Send a request to a supervisor's Unix socket and return the reply.
 * Used for status probes and live message injection.
 */
export function trySocketRequest(sockPath, request, timeoutMs = 2000) {
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

/**
 * Compute the socket path for a supervisor instance.
 */
export function sockPathFor(msgDir, instance) {
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
