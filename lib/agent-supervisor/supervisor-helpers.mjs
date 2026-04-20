/**
 * Pure helpers extracted from supervisor.mjs so tests can run without the SDK.
 * These functions have no external dependencies (only node:child_process).
 */

import { execFile } from 'node:child_process'
import { promisify } from 'node:util'

const execFileAsync = promisify(execFile)

const GIT_BRANCH_TTL_MS = 2000

/**
 * Derive role-dependent behavior. Pure function for testability.
 */
export function deriveRoleBehavior({ role, instance, persistent }) {
  const isMultiTurn = role === 'coordinator' || role === 'advisor' || persistent === true
  const socketInstance = role === 'coordinator' ? '_coordinator' : instance
  const eventSubdir = role === 'coordinator' ? '_coordinator' : instance
  return { isMultiTurn, socketInstance, eventSubdir }
}

/**
 * Build the extra fields to merge into the supervisor's status reply.
 * `cache` is a mutable object ({lastBranch, lastBranchTs}) used to cache
 * the git branch result across rapid-fire calls. `now` is injectable for tests.
 */
export async function computeStatusExtras({ queueLength, cwd, cache, now }) {
  cache = cache || computeStatusExtras.defaultCache
  now = now || Date.now

  const extras = { queue_depth: queueLength }

  const t = now()
  if (cache.lastBranch !== null && t - cache.lastBranchTs < GIT_BRANCH_TTL_MS) {
    extras.git_branch = cache.lastBranch
  } else {
    let branch = 'unknown'
    try {
      const { stdout } = await execFileAsync('git', ['-C', cwd, 'rev-parse', '--abbrev-ref', 'HEAD'], {
        timeout: 1000,
      })
      branch = stdout.trim() || 'unknown'
    } catch {
      branch = 'unknown'
    }
    cache.lastBranch = branch
    cache.lastBranchTs = t
    extras.git_branch = branch
  }

  return extras
}
computeStatusExtras.defaultCache = { lastBranch: null, lastBranchTs: 0 }

/**
 * Construct a user message in SDK format.
 */
export function makeUserMessage(text, sessionId = '') {
  return {
    type: 'user',
    session_id: sessionId,
    parent_tool_use_id: null,
    message: {
      role: 'user',
      content: [{ type: 'text', text }],
    },
  }
}

/**
 * Handle a single supervisor socket request. Exported for tests.
 * `state` = { promptQueue, cwd, getQuery, getStatus, onShutdown }
 */
export async function handleSupervisorRequest(req, state) {
  switch (req.cmd) {
    case 'send_user_message': {
      if (typeof req.text !== 'string' || !req.text) {
        return { ok: false, error: 'text required' }
      }
      state.promptQueue.push(makeUserMessage(req.text))
      return { ok: true }
    }
    case 'interrupt': {
      const q = state.getQuery()
      if (!q) return { ok: false, error: 'query not yet started' }
      try {
        await q.interrupt()
        return { ok: true }
      } catch (e) {
        return { ok: false, error: e.message }
      }
    }
    case 'status': {
      const base = state.getStatus()
      const extras = await computeStatusExtras({
        queueLength: state.promptQueue._queue.length,
        cwd: state.cwd,
      })
      return { ok: true, status: { ...base, ...extras } }
    }
    case 'shutdown':
    case 'shutdown_self':
      state.onShutdown()
      return { ok: true }
    default:
      return { ok: false, error: `unknown cmd: ${req.cmd}` }
  }
}
