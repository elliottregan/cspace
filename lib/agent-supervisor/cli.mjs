#!/usr/bin/env node

/**
 * cli.mjs — host-side dispatcher for the agent-supervisor.
 *
 * Replaces agent-messages.sh. Each subcommand prefers the supervisor's
 * Unix socket (instant delivery, injected as a new user turn) and falls
 * back to the filesystem message queue for the cross-container coordinator
 * path or when no supervisor is running.
 *
 * Usage:
 *   cli.mjs send <instance> "text"
 *   cli.mjs respond <instance> <question-id> "text"
 *   cli.mjs list [instance]
 *   cli.mjs watch [instance]
 *   cli.mjs interrupt <instance>
 *   cli.mjs status <instance>
 */

import { execSync } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import fs from 'node:fs'
import net from 'node:net'
import path from 'node:path'

// --- Message dir resolution ---
//
// Prefer /logs/messages (present in every cspace container because the
// shared logs volume is mounted everywhere). Fall back to `docker volume
// inspect` on the host-side path. The volume name is configurable via
// CSPACE_LOGS_VOLUME (set by bin/cspace from the project config).

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

// --- Filesystem helpers (fallback path) ---

function makeId() {
  return `msg-${Date.now()}-${randomUUID().slice(0, 8)}`
}

function writeMessageFile(dir, message) {
  fs.mkdirSync(dir, { recursive: true })
  const file = `${message.id}.json`
  const tmp = path.join(dir, `.${file}.tmp`)
  fs.writeFileSync(tmp, JSON.stringify(message, null, 2))
  fs.renameSync(tmp, path.join(dir, file))
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

function listInstances() {
  if (!fs.existsSync(MSG_DIR)) return []
  return fs
    .readdirSync(MSG_DIR)
    .filter((d) => !d.startsWith('_') && fs.statSync(path.join(MSG_DIR, d)).isDirectory())
}

function isAnswered(instance, questionId) {
  return readMessages(path.join(MSG_DIR, instance, 'inbox')).some(
    (m) => m.in_reply_to === questionId && m.type === 'response',
  )
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
  // Fallback: write a directive to the inbox so a polling agent (legacy)
  // or the agent's next restart can pick it up.
  const directive = {
    id: makeId(),
    timestamp: new Date().toISOString(),
    from: 'coordinator',
    type: 'directive',
    content: text,
  }
  writeMessageFile(path.join(MSG_DIR, instance, 'inbox'), directive)
  console.log(
    `Socket unavailable (${socketReply.error}), wrote directive ${directive.id} to ${instance}/inbox.`,
  )
}

async function cmdRespond(instance, questionId, text) {
  if (!instance || !questionId || !text) {
    die('Usage: cli.mjs respond <instance> <question-id> "text"')
  }
  // Always write the filesystem reply first — it unblocks agent-side
  // ask_orchestrator tools that are fs.watch-blocked, and supervisor-less
  // agents rely on it entirely.
  const response = {
    id: makeId(),
    timestamp: new Date().toISOString(),
    from: 'coordinator',
    type: 'response',
    in_reply_to: questionId,
    content: text,
  }
  writeMessageFile(path.join(MSG_DIR, instance, 'inbox'), response)

  // Best-effort: also push a user turn to the supervisor so live claude
  // sessions see the reply immediately as in-conversation context.
  const socketReply = await trySocketRequest(sockPathFor(instance), {
    cmd: 'respond_to_question',
    questionId,
    text,
  })
  if (socketReply.ok) {
    console.log(`Reply sent to ${instance} (file + socket).`)
  } else {
    console.log(`Reply written to ${instance}/inbox (socket unavailable: ${socketReply.error}).`)
  }
}

function cmdList(instance) {
  const instances = instance ? [instance] : listInstances()
  let found = 0
  for (const inst of instances) {
    for (const msg of readMessages(path.join(MSG_DIR, inst, 'outbox'))) {
      if (msg.type !== 'question') continue
      if (isAnswered(inst, msg.id)) continue
      if (found === 0) {
        process.stdout.write('INSTANCE         ID                            URGENCY    QUESTION\n')
        process.stdout.write('--------         --                            -------    --------\n')
      }
      found += 1
      process.stdout.write(
        `${inst.padEnd(17)}${msg.id.padEnd(30)}${(msg.urgency || 'blocking').padEnd(11)}${msg.content}\n`,
      )
      if (msg.context) process.stdout.write(`${' '.repeat(58)}Context: ${msg.context}\n`)
    }
  }
  if (found === 0) console.log('(no pending questions)')
}

function cmdWatch(instance) {
  const seen = new Set()
  const printNew = (inst) => {
    const outbox = path.join(MSG_DIR, inst, 'outbox')
    for (const msg of readMessages(outbox)) {
      if (seen.has(msg.id)) continue
      seen.add(msg.id)
      if (msg.type === 'question') {
        console.log(`[${msg.timestamp}] ${inst} asks: ${msg.content}`)
        console.log(`  → cspace respond ${inst} ${msg.id} "your answer"`)
        console.log('')
      } else if (msg.type === 'notification') {
        console.log(
          `[${msg.timestamp}] ${inst} (${msg.notificationType || 'progress'}): ${msg.content}`,
        )
      }
    }
  }

  const targets = instance ? [instance] : listInstances()
  for (const inst of targets) {
    const outbox = path.join(MSG_DIR, inst, 'outbox')
    fs.mkdirSync(outbox, { recursive: true })
    printNew(inst) // flush whatever is already there (mark as seen)
    try {
      fs.watch(outbox, () => printNew(inst))
    } catch (e) {
      console.error(`[cli] watch failed on ${outbox}: ${e.message}`)
    }
  }
  console.log(`Watching ${targets.length || 'all'} instance(s)... (Ctrl+C to stop)`)
  // Keep process alive.
  setInterval(() => {}, 1 << 30)
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
  case 'respond':
    await cmdRespond(rest[0], rest[1], rest[2])
    break
  case 'list':
  case 'messages':
    cmdList(rest[0])
    break
  case 'watch':
    cmdWatch(rest[0])
    break
  case 'interrupt':
    await cmdInterrupt(rest[0])
    break
  case 'status':
    await cmdStatus(rest[0])
    break
  default:
    die(
      `Usage: cli.mjs {send|respond|list|watch|interrupt|status} ...
  send <instance> "text"
  respond <instance> <question-id> "text"
  list [instance]
  watch [instance]
  interrupt <instance>
  status <instance>`,
    )
}
