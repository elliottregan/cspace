import { test } from 'node:test'
import assert from 'node:assert/strict'
import { parseArgsForTest } from './args.mjs'

test('--advisors accepts comma-separated list', () => {
  const args = parseArgsForTest([
    'node', 'supervisor.mjs',
    '--role', 'agent',
    '--instance', 'issue-42',
    '--prompt-file', '/tmp/p',
    '--advisors', 'decision-maker,critic',
  ])
  assert.deepEqual(args.advisors, ['decision-maker', 'critic'])
})

test('--advisors absent yields empty list', () => {
  const args = parseArgsForTest([
    'node', 'supervisor.mjs',
    '--role', 'agent',
    '--instance', 'issue-42',
    '--prompt-file', '/tmp/p',
  ])
  assert.deepEqual(args.advisors, [])
})
