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
 * @param {object} sdkMessage
 * @returns {string | null} NDJSON line to emit, or null to skip.
 */
export function shim(sdkMessage) {
  if (!sdkMessage || typeof sdkMessage !== 'object') return null

  if (sdkMessage.type === 'result') {
    const shimmed = { ...sdkMessage }
    if (shimmed.cost_usd == null && shimmed.total_cost_usd != null) {
      shimmed.cost_usd = shimmed.total_cost_usd
    }
    return JSON.stringify(shimmed)
  }

  return JSON.stringify(sdkMessage)
}
