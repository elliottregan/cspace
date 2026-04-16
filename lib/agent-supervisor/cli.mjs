#!/usr/bin/env node

/**
 * cli.mjs — host-side dispatcher for the agent-supervisor.
 *
 * All commands go through the supervisor's Unix socket for instant delivery.
 *
 * Usage:
 *   cli.mjs send <instance> "text"     Inject a user turn
 *   cli.mjs interrupt <instance>       Interrupt the current tool call
 *   cli.mjs status <instance>          Show supervisor status as JSON
 */

import { execSync } from 'node:child_process'
import fs from 'node:fs'
import net from 'node:net'
import path from 'node:path'

// --- Message dir resolution ---

function resolveMsgDir() {
  if (process.env.CLAUDE_MSG_DIR) return process.env.CLAUDE_MSG_DIR
  if (fs.existsSync('/logs/messages')) return '/logs/messages'
  const logsVolume = process.env.CSPACE_LOGS_VOLUME || 'cspace-logs'
  try {
    const mountpoint = execSync(
      `docker volume inspect ${logsVolume} --format "{{ .Mountpoint }}"`,
      { stdio: ['ignore', 'pipe', 'ignore'] },
    )
      .toString()
      .trim()
    if (mountpoint) return path.join(mountpoint, 'messages')
  } catch {}
  throw new Error(
    `Could not resolve message directory. Start an agent instance first, or set CLAUDE_MSG_DIR (logs volume: ${logsVolume}).`,
  )
}

const MSG_DIR = resolveMsgDir()

function sockPathFor(instance) {
  if (instance === '_coordinator') {
    return path.join(MSG_DIR, '_coordinator', 'supervisor.sock')
  }
  return path.join(MSG_DIR, instance, 'supervisor.sock')
}

// --- Socket client ---

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
    const timer = setTimeout(() => finish({ ok: false, error: 'timeout' }), timeoutMs)
    let buf = ''
    conn.on('connect', () => conn.write(JSON.stringify(request) + '\n'))
    conn.on('data', (chunk) => {
      buf += chunk.toString('utf-8')
      const idx = buf.indexOf('\n')
      if (idx >= 0) {
        try {
          finish(JSON.parse(buf.slice(0, idx)))
        } catch (e) {
          finish({ ok: false, error: `bad reply: ${e.message}` })
        }
      }
    })
    conn.on('error', (e) => finish({ ok: false, error: e.message }))
    conn.on('end', () => finish({ ok: false, error: 'connection closed' }))
  })
}

// --- Commands ---

async function cmdSend(instance, text) {
  if (!instance || !text) die('Usage: cli.mjs send <instance> "text"')
  const socketReply = await trySocketRequest(sockPathFor(instance), {
    cmd: 'send_user_message',
    text,
    source: 'human',
  })
  if (socketReply.ok) {
    console.log(`Injected user turn into ${instance} (socket).`)
    return
  }
  die(
    `Could not reach ${instance} — supervisor socket unavailable (${socketReply.error}). ` +
      `The agent may have crashed or exited. Check: cspace agent-status ${instance}`,
  )
}

async function cmdInterrupt(instance) {
  if (!instance) die('Usage: cli.mjs interrupt <instance>')
  const reply = await trySocketRequest(sockPathFor(instance), { cmd: 'interrupt' })
  if (reply.ok) {
    console.log(`Interrupted ${instance}.`)
  } else {
    console.error(`Interrupt failed: ${reply.error}`)
    process.exit(1)
  }
}

async function cmdStatus(instance) {
  if (!instance) die('Usage: cli.mjs status <instance>')
  const reply = await trySocketRequest(sockPathFor(instance), { cmd: 'status' })
  if (reply.ok) {
    console.log(JSON.stringify(reply.status, null, 2))
  } else {
    console.error(`Status failed: ${reply.error}`)
    process.exit(1)
  }
}

function die(msg) {
  console.error(msg)
  process.exit(2)
}

// --- Dispatch ---

const [, , cmd, ...rest] = process.argv
switch (cmd) {
  case 'send':
    await cmdSend(rest[0], rest[1])
    break
  case 'interrupt':
    await cmdInterrupt(rest[0])
    break
  case 'status':
    await cmdStatus(rest[0])
    break
  default:
    die(
      `Usage: cli.mjs {send|interrupt|status} ...
  send <instance> "text"       Inject a user turn into an agent session
  interrupt <instance>         Interrupt the current tool call
  status <instance>            Show supervisor status as JSON`,
    )
}
