import { test } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import {
  filterStreamForRead,
  findLatestSessionFile,
  readAllEnvelopes,
  readTailEnvelopes,
} from './event-log.mjs'

/** Create a temp dir with an NDJSON session file prepopulated. */
function makeFixture({ instance = 'mars', lines = [] } = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'cspace-evlog-'))
  const dir = path.join(root, instance)
  fs.mkdirSync(dir, { recursive: true })
  const file = path.join(dir, 'session-2026-04-15-abc.ndjson')
  fs.writeFileSync(file, lines.map((l) => JSON.stringify(l)).join('\n') + (lines.length ? '\n' : ''))
  return { root, file, instance }
}

function mkEnv(ts, type = 'assistant', extra = {}) {
  return { ts, instance: 'mars', role: 'agent', sdk: { type, ...extra } }
}

test('findLatestSessionFile returns null when dir missing', () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'cspace-evlog-'))
  assert.equal(findLatestSessionFile(root, 'nonexistent'), null)
})

test('findLatestSessionFile ignores files without session- prefix or .ndjson suffix', () => {
  const { root, instance } = makeFixture({ lines: [mkEnv('2026-04-15T00:00:00Z')] })
  const dir = path.join(root, instance)
  fs.writeFileSync(path.join(dir, 'random.txt'), 'junk')
  fs.writeFileSync(path.join(dir, 'session-wrong.json'), '{}')
  const found = findLatestSessionFile(root, instance)
  assert.ok(found.endsWith('.ndjson'))
  assert.ok(path.basename(found).startsWith('session-'))
})

test('findLatestSessionFile returns newest by mtime when multiple sessions exist', () => {
  const { root, instance } = makeFixture({ lines: [mkEnv('2026-04-15T00:00:00Z')] })
  const dir = path.join(root, instance)
  const newer = path.join(dir, 'session-2026-04-15-xyz.ndjson')
  fs.writeFileSync(newer, '{}\n')
  // Ensure mtime ordering: bump newer's mtime into the future.
  const t = Date.now() / 1000 + 10
  fs.utimesSync(newer, t, t)
  const found = findLatestSessionFile(root, instance)
  assert.equal(path.basename(found), 'session-2026-04-15-xyz.ndjson')
})

test('readAllEnvelopes parses each line; silently skips malformed', () => {
  const { file } = makeFixture({
    lines: [mkEnv('2026-04-15T00:00:00Z'), mkEnv('2026-04-15T00:00:01Z')],
  })
  fs.appendFileSync(file, 'not-json\n')
  fs.appendFileSync(file, JSON.stringify(mkEnv('2026-04-15T00:00:02Z')) + '\n')
  const envs = readAllEnvelopes(file)
  assert.equal(envs.length, 3)
  assert.equal(envs[2].ts, '2026-04-15T00:00:02Z')
})

test('readAllEnvelopes returns [] for missing file', () => {
  assert.deepEqual(readAllEnvelopes('/does/not/exist.ndjson'), [])
})

test('readTailEnvelopes returns the last N envelopes', () => {
  const lines = []
  for (let i = 0; i < 20; i++) lines.push(mkEnv(`2026-04-15T00:00:${String(i).padStart(2, '0')}Z`))
  const { file } = makeFixture({ lines })
  const tail = readTailEnvelopes(file, 5)
  assert.equal(tail.length, 5)
  assert.equal(tail[0].ts, '2026-04-15T00:00:15Z')
  assert.equal(tail[4].ts, '2026-04-15T00:00:19Z')
})

test('filterStreamForRead without `since` returns the tail up to limit, chronologically', () => {
  const envelopes = []
  for (let i = 0; i < 10; i++)
    envelopes.push(mkEnv(`2026-04-15T00:00:${String(i).padStart(2, '0')}Z`))
  const out = filterStreamForRead({ envelopes, limit: 3 })
  assert.equal(out.returned, 3)
  assert.equal(out.total, 10)
  assert.equal(out.truncated, true)
  assert.equal(out.envelopes[0].ts, '2026-04-15T00:00:07Z')
  assert.equal(out.envelopes[2].ts, '2026-04-15T00:00:09Z')
  assert.equal(out.last_ts, '2026-04-15T00:00:09Z')
})

test('filterStreamForRead with `since` returns strictly-later envelopes oldest-first', () => {
  const envelopes = [
    mkEnv('2026-04-15T00:00:00Z'),
    mkEnv('2026-04-15T00:00:05Z'),
    mkEnv('2026-04-15T00:00:10Z'),
    mkEnv('2026-04-15T00:00:15Z'),
  ]
  const out = filterStreamForRead({
    envelopes,
    since: '2026-04-15T00:00:05Z',
    limit: 100,
  })
  assert.equal(out.returned, 2)
  assert.equal(out.envelopes[0].ts, '2026-04-15T00:00:10Z')
  assert.equal(out.envelopes[1].ts, '2026-04-15T00:00:15Z')
  assert.equal(out.last_ts, '2026-04-15T00:00:15Z')
  assert.equal(out.truncated, false)
})

test('filterStreamForRead with `since` and limit caps forward-scan', () => {
  const envelopes = []
  for (let i = 0; i < 10; i++)
    envelopes.push(mkEnv(`2026-04-15T00:00:${String(i).padStart(2, '0')}Z`))
  const out = filterStreamForRead({
    envelopes,
    since: '2026-04-15T00:00:02Z',
    limit: 3,
  })
  assert.equal(out.returned, 3)
  assert.equal(out.envelopes[0].ts, '2026-04-15T00:00:03Z')
  assert.equal(out.envelopes[2].ts, '2026-04-15T00:00:05Z')
  assert.equal(out.last_ts, '2026-04-15T00:00:05Z')
  assert.equal(out.truncated, true)
})

test('filterStreamForRead with `since` and no new events returns empty + echoes `since` as last_ts', () => {
  const envelopes = [mkEnv('2026-04-15T00:00:00Z')]
  const out = filterStreamForRead({
    envelopes,
    since: '2026-04-15T00:00:00Z',
    limit: 10,
  })
  assert.equal(out.returned, 0)
  assert.equal(out.last_ts, '2026-04-15T00:00:00Z')
})

test('filterStreamForRead `types` filter applies before slicing', () => {
  const envelopes = [
    mkEnv('2026-04-15T00:00:00Z', 'assistant'),
    mkEnv('2026-04-15T00:00:01Z', 'user'),
    mkEnv('2026-04-15T00:00:02Z', 'assistant'),
    mkEnv('2026-04-15T00:00:03Z', 'result'),
    mkEnv('2026-04-15T00:00:04Z', 'assistant'),
  ]
  const out = filterStreamForRead({ envelopes, limit: 10, types: ['assistant'] })
  assert.equal(out.returned, 3)
  assert.ok(out.envelopes.every((e) => e.sdk.type === 'assistant'))
})

test('filterStreamForRead returns last_ts=null for empty input without `since`', () => {
  const out = filterStreamForRead({ envelopes: [], limit: 10 })
  assert.equal(out.returned, 0)
  assert.equal(out.total, 0)
  assert.equal(out.last_ts, null)
})
