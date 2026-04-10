/**
 * stream-shim — translate SDKMessage into the NDJSON shape that
 * stream-status.sh expects from `claude --output-format stream-json`.
 *
 * The SDK emits messages that are 99% identical to the CLI's stream-json
 * output. The one notable drift is `result` messages: the SDK uses
 * `total_cost_usd`, while stream-status.sh reads `.cost_usd`. Everything
 * else (system/init, assistant, user, tool_use) passes through unchanged;
 * stream-status.sh tolerates extra fields and ignores unknown message
 * types (rate_limit_event, hook_started, hook_response, …).
 */

/**
 * Normalize an SDKMessage into the object shape stream-status.sh expects.
 * Returns null for falsy/non-object input. Exposed separately from `shim`
 * so the supervisor can persist the normalized object into its event log
 * without a JSON parse round-trip on every hot-loop message.
 *
 * @param {object} sdkMessage
 * @returns {object | null}
 */
export function normalizeSdkMessage(sdkMessage) {
  if (!sdkMessage || typeof sdkMessage !== 'object') return null

  if (sdkMessage.type === 'result') {
    const normalized = { ...sdkMessage }
    if (normalized.cost_usd == null && normalized.total_cost_usd != null) {
      normalized.cost_usd = normalized.total_cost_usd
    }
    return normalized
  }

  return sdkMessage
}

/**
 * @param {object} sdkMessage
 * @returns {string | null} NDJSON line to emit, or null to skip.
 */
export function shim(sdkMessage) {
  const normalized = normalizeSdkMessage(sdkMessage)
  return normalized == null ? null : JSON.stringify(normalized)
}
