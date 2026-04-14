import { test } from 'node:test'
import assert from 'node:assert/strict'
// Import from args.mjs (the SDK-free module) so these tests can run
// without node_modules/ installed. supervisor.mjs re-exports the same
// names for runtime callers.
import { parseArgsForTest, buildQueryOptions } from './args.mjs'

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
    model: 'claude-opus-4-6',
    cwd: '/workspace',
    mcpServers: {},
    allowedTools: [],
  })
  assert.equal(opts.resume, undefined)
  assert.equal(opts.model, 'claude-opus-4-6')
  assert.equal(opts.permissionMode, 'bypassPermissions')
})

test('buildQueryOptions with resumeSession sets resume', () => {
  const opts = buildQueryOptions({
    model: 'claude-opus-4-6',
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
