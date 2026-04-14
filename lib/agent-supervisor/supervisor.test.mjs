import { test } from 'node:test'
import assert from 'node:assert/strict'
import { parseArgsForTest } from './supervisor.mjs'

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
