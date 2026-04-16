/**
 * Pure helpers for reading supervisor event logs.
 *
 * Event logs are NDJSON files at /logs/events/{instance}/session-*.ndjson.
 * Each line is an envelope { ts, instance, role, sdk } where `sdk` is the
 * raw Agent SDK message (assistant turn, tool_use, tool_result, result,
 * thinking block, etc.).
 *
 * This module is intentionally free of SDK imports so its exports can be
 * unit-tested without node_modules/ installed (matches supervisor.test.mjs
 * convention). The SDK-facing MCP tool wrappers live in sdk-mcp-tools.mjs
 * and delegate their heavy lifting here.
 */

import fs from 'node:fs'
import path from 'node:path'

/**
 * Find the newest session-*.ndjson file for an instance, by mtime.
 * Returns null if the directory or any matching file is missing.
 */
export function findLatestSessionFile(eventLogRoot, instance) {
  if (!eventLogRoot || !instance) return null
  const dir = path.join(eventLogRoot, instance)
  if (!fs.existsSync(dir)) return null
  let newest = null
  let newestMtime = 0
  try {
    for (const f of fs.readdirSync(dir)) {
      if (!f.startsWith('session-') || !f.endsWith('.ndjson')) continue
      const fp = path.join(dir, f)
      const mtime = fs.statSync(fp).mtimeMs
      if (mtime > newestMtime) {
        newestMtime = mtime
        newest = fp
      }
    }
  } catch {}
  return newest
}

/** Parse an NDJSON file into envelopes; malformed lines are silently skipped. */
export function readAllEnvelopes(filePath) {
  try {
    const raw = fs.readFileSync(filePath, 'utf-8')
    const parsed = []
    for (const line of raw.split('\n')) {
      if (!line.trim()) continue
      try {
        parsed.push(JSON.parse(line))
      } catch {}
    }
    return parsed
  } catch {
    return []
  }
}

/** Read the last N parsed envelopes from an NDJSON file. */
export function readTailEnvelopes(filePath, maxLines) {
  const all = readAllEnvelopes(filePath)
  return maxLines != null ? all.slice(-maxLines) : all
}

/**
 * Filter/slice envelopes for the read_agent_stream MCP tool.
 *
 *   since  — ISO timestamp string. If set, return only envelopes with ts > since,
 *            chronologically (oldest-first). Intended for incremental polling:
 *            pass the previous call's `last_ts` back in.
 *          — If omitted, return the tail (most recent `limit` envelopes,
 *            still chronologically ordered).
 *   limit  — max envelopes to return.
 *   types  — optional allowlist filter on envelope.sdk.type.
 *
 * Returns { envelopes, returned, total, truncated, last_ts }.
 */
export function filterStreamForRead({ envelopes, since, limit, types }) {
  const total = envelopes.length

  let filtered = envelopes
  if (since) {
    filtered = filtered.filter((e) => e && e.ts && e.ts > since)
  }
  if (types && types.length > 0) {
    filtered = filtered.filter((e) => e && e.sdk && types.includes(e.sdk.type))
  }

  // `since` given → take oldest `limit` (chronological forward-scan).
  // `since` absent → take newest `limit` (tail), but still chronological order.
  const truncated = filtered.length > limit
  const result = since ? filtered.slice(0, limit) : filtered.slice(-limit)

  const lastEnv = result.length > 0 ? result[result.length - 1] : null
  const lastTs = lastEnv && lastEnv.ts ? lastEnv.ts : since || null

  return {
    envelopes: result,
    returned: result.length,
    total,
    truncated,
    last_ts: lastTs,
  }
}
