import { test, before, after } from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import fs from 'node:fs'
import path from 'node:path'
import os from 'node:os'
import {
  sendAndFetchStatus,
} from './sdk-mcp-tools.mjs'

const enabled = process.env.RUN_INTEGRATION === '1'

function makeFakeSupervisor(sockPath, statusReply) {
  const received = []
  const srv = net.createServer((conn) => {
    let buf = ''
    conn.on('data', (chunk) => {
      buf += chunk.toString('utf-8')
      while (true) {
        const idx = buf.indexOf('\n')
        if (idx < 0) break
        const line = buf.slice(0, idx)
        buf = buf.slice(idx + 1)
        try {
          const req = JSON.parse(line)
          received.push(req)
          if (req.cmd === 'send_user_message') {
            conn.write(JSON.stringify({ ok: true }) + '\n')
          } else if (req.cmd === 'status') {
            conn.write(JSON.stringify({ ok: true, status: statusReply }) + '\n')
          }
        } catch (e) {
          conn.write(JSON.stringify({ ok: false, error: e.message }) + '\n')
        }
      }
    })
  })
  return new Promise((resolve) =>
    srv.listen(sockPath, () => resolve({ srv, received })),
  )
}

test('send_to_advisor round-trip delivers and reports status', { skip: !enabled }, async () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'advisor-int-'))
  const sockPath = path.join(tmp, 'supervisor.sock')
  const { srv, received } = await makeFakeSupervisor(sockPath, {
    role: 'advisor',
    instance: 'decision-maker',
    sessionId: 'session-1',
    turns: 3,
    lastActivityMs: 0,
    queue_depth: 0,
    git_branch: 'main',
  })
  try {
    const r = await sendAndFetchStatus(sockPath, 'test message')
    assert.equal(r.delivered, true)
    assert.equal(r.status.instance, 'decision-maker')
    assert.equal(r.status.git_branch, 'main')
    assert.deepEqual(
      received.map((req) => req.cmd),
      ['send_user_message', 'status'],
    )
  } finally {
    srv.close()
    fs.rmSync(tmp, { recursive: true, force: true })
  }
})
