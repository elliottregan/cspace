import { test } from 'node:test'
import assert from 'node:assert/strict'
// Import from args.mjs (the SDK-free module) so these tests can run
// without node_modules/ installed. supervisor.mjs re-exports the same
// names for runtime callers.
import { parseArgsForTest, buildQueryOptions } from './args.mjs'
import { computeStatusExtras } from './supervisor.mjs'

test('parses --resume-session', () => {
  const args = parseArgsForTest(['node', 'supervisor.mjs',
    '--role', 'agent',
    '--resume-session', 'abc-123',
    '--instance', 'mars',
  ])
  assert.equal(args.resumeSession, 'abc-123')
  assert.equal(args.promptFile, undefined)
})

test('rejects resume + prompt-file combination', () => {
  assert.throws(() => parseArgsForTest(['node', 'supervisor.mjs',
    '--role', 'agent',
    '--resume-session', 'abc-123',
    '--prompt-file', '/tmp/p.txt',
  ]), /resume-session.*prompt-file/i)
})

test('parses --persistent', () => {
  const args = parseArgsForTest(['node', 'supervisor.mjs',
    '--role', 'agent',
    '--instance', 'venus',
    '--prompt-file', '/tmp/p.txt',
    '--persistent',
  ])
  assert.equal(args.persistent, true)
})

test('persistent defaults to falsy when not passed', () => {
  const args = parseArgsForTest(['node', 'supervisor.mjs',
    '--role', 'agent',
    '--instance', 'venus',
    '--prompt-file', '/tmp/p.txt',
  ])
  assert.equal(Boolean(args.persistent), false)
})

test('parses --prompt-file without resume', () => {
  const args = parseArgsForTest(['node', 'supervisor.mjs',
    '--role', 'agent',
    '--prompt-file', '/tmp/p.txt',
    '--instance', 'mars',
  ])
  assert.equal(args.promptFile, '/tmp/p.txt')
  assert.equal(args.resumeSession, undefined)
})

test('buildQueryOptions without resumeSession omits resume', () => {
  const opts = buildQueryOptions({
    model: 'claude-opus-4-7',
    cwd: '/workspace',
    mcpServers: {},
    allowedTools: [],
  })
  assert.equal(opts.resume, undefined)
  assert.equal(opts.model, 'claude-opus-4-7')
  assert.equal(opts.permissionMode, 'bypassPermissions')
})

test('buildQueryOptions with resumeSession sets resume', () => {
  const opts = buildQueryOptions({
    model: 'claude-opus-4-7',
    cwd: '/workspace',
    mcpServers: {},
    allowedTools: [],
    resumeSession: 'abc-123',
  })
  assert.equal(opts.resume, 'abc-123')
})

test('buildQueryOptions forwards appendSystemPrompt when set', () => {
  const opts = buildQueryOptions({
    model: 'x',
    cwd: '/w',
    mcpServers: {},
    allowedTools: [],
    appendSystemPrompt: 'extra',
  })
  assert.equal(opts.appendSystemPrompt, 'extra')
})

test('buildQueryOptions omits appendSystemPrompt when not set', () => {
  const opts = buildQueryOptions({
    model: 'x',
    cwd: '/w',
    mcpServers: {},
    allowedTools: [],
  })
  assert.equal('appendSystemPrompt' in opts, false)
})

test('buildQueryOptions omits model when undefined so SDK uses account default', () => {
  const opts = buildQueryOptions({
    cwd: '/w',
    mcpServers: {},
    allowedTools: [],
  })
  assert.equal('model' in opts, false)
})

test('buildQueryOptions forwards effort when set', () => {
  const opts = buildQueryOptions({
    cwd: '/w',
    mcpServers: {},
    allowedTools: [],
    effort: 'max',
  })
  assert.equal(opts.effort, 'max')
})

test('buildQueryOptions omits effort when undefined', () => {
  const opts = buildQueryOptions({
    cwd: '/w',
    mcpServers: {},
    allowedTools: [],
  })
  assert.equal('effort' in opts, false)
})

test('computeStatusExtras returns queue_depth and git_branch', async () => {
  const extras = await computeStatusExtras({
    queueLength: 3,
    cwd: process.cwd(),
  })
  assert.equal(extras.queue_depth, 3)
  assert.equal(typeof extras.git_branch, 'string')
})

test('computeStatusExtras caches git_branch within TTL', async () => {
  const state = { lastBranch: null, lastBranchTs: 0 }
  const first = await computeStatusExtras({
    queueLength: 0,
    cwd: process.cwd(),
    cache: state,
    now: () => 1000,
  })
  assert.ok(state.lastBranchTs === 1000)
  const second = await computeStatusExtras({
    queueLength: 0,
    cwd: '/does/not/exist',
    cache: state,
    now: () => 1500, // within 2s TTL
  })
  assert.equal(second.git_branch, first.git_branch, 'should reuse cached branch')
})

test('shutdown_self closes the prompt queue', async () => {
  const { handleSupervisorRequest } = await import('./supervisor.mjs')
  let shutdownCalled = false
  const state = {
    promptQueue: { push: () => {}, close: () => {}, _queue: [] },
    cwd: process.cwd(),
    onShutdown: () => { shutdownCalled = true },
    getQuery: () => null,
    getStatus: () => ({ role: 'agent', instance: 'test', sessionId: '', turns: 0, lastActivityMs: 0 }),
  }
  const reply = await handleSupervisorRequest({ cmd: 'shutdown_self' }, state)
  assert.equal(reply.ok, true)
  assert.equal(shutdownCalled, true)
})
