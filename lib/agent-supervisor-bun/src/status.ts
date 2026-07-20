// Derives the coarse steering state GET /status reports (main.ts) from the
// `type` of the last SDK event the supervisor has observed. Per sdk.d.ts's
// SDKMessage taxonomy, `type: 'result'` (SDKResultMessage, subtypes
// 'success' | 'error_during_execution' | 'error_max_turns' | ...) is the only
// event that marks a turn as FINISHED — every other member of the union
// (assistant/user/system/status/tool-progress/task-*/etc.) is emitted while
// a turn is still in flight. Before the first event has landed at all
// (lastEventType undefined — e.g. GET /status hit right after boot, before
// any /send), there is by definition no in-flight turn either, so that's
// idle too.
export function deriveState(lastEventType: string | undefined): "working" | "idle" {
  if (lastEventType === undefined || lastEventType === "result") {
    return "idle";
  }
  return "working";
}
