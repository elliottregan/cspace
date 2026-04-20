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

test('worker role exposes notify_coordinator, ask_coordinator, shutdown_self', async () => {
  const { buildMessengerMcpServer } = await import('./sdk-mcp-tools.mjs')
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

test('worker role exposes handshake_advisor and ask_advisor when advisors are configured', async () => {
  const { buildMessengerMcpServer } = await import('./sdk-mcp-tools.mjs')
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

test('coordinator role exposes ask_advisor and send_to_advisor when advisors are configured', async () => {
  const { buildMessengerMcpServer } = await import('./sdk-mcp-tools.mjs')
  const { toolNames } = buildMessengerMcpServer({
    role: 'coordinator',
    msgDir: '/tmp',
    eventLogRoot: '/tmp',
    advisorNames: ['decision-maker'],
  })
  assert.ok(toolNames.includes('mcp__agent-messenger__ask_advisor'))
  assert.ok(toolNames.includes('mcp__agent-messenger__send_to_advisor'))
})

test('advisor tools are omitted when no advisors configured', async () => {
  const { buildMessengerMcpServer } = await import('./sdk-mcp-tools.mjs')
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
