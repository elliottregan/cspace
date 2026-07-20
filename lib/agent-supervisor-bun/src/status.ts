// Derives the coarse steering state GET /status reports (main.ts) from the
// `type` (and, for system events, `subtype`) of the last SDK event the
// supervisor has observed. Per sdk.d.ts's SDKMessage taxonomy, `type:
// 'result'` (SDKResultMessage, subtypes 'success' | 'error_during_execution'
// | 'error_max_turns' | ...) is the only event that marks a turn as FINISHED
// — every other member of the union (assistant/user/system/status/
// tool-progress/task-*/etc.) is emitted while a turn is still in flight,
// with one exception: SDKSystemMessage subtype 'init' fires at session
// start (fresh boot or respawn-resume) BEFORE any prompt, so an agent whose
// last event is system/init is idle, not working. Other system subtypes
// (api_retry, compact_boundary) fire mid-turn and stay working. Before the
// first event has landed at all (lastEventType undefined — e.g. GET /status
// hit right after boot, before any /send), there is by definition no
// in-flight turn either, so that's idle too.
export function deriveState(
  lastEventType: string | undefined,
  lastEventSubtype?: string,
): "working" | "idle" {
  if (lastEventType === undefined || lastEventType === "result") {
    return "idle";
  }
  if (lastEventType === "system" && lastEventSubtype === "init") {
    return "idle";
  }
  return "working";
}
