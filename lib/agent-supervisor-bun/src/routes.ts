// Route-logic helpers extracted out of main.ts so they can be unit tested
// without spinning up Bun.serve. Kept as pure(ish) functions that map inputs
// to a {status, body} pair; main.ts's fetch handler is a thin caller that
// forwards the result to Response.json.

export interface RouteResult {
  status: number;
  body: { ok: boolean; error?: string };
}

// POST /interrupt. `q` is the live SDK query handle (or undefined if no task
// is in flight — see main.ts's currentQuery lifecycle comment). The SDK's
// interrupt() can reject when the transport is already closed or dying (e.g.
// a request that read a truthy handle just before it was cleared on settle):
// a rejecting interrupt means the task is effectively gone, so that case is
// folded into the same 409 "no active task" semantics as the no-handle case,
// rather than letting the rejection escape to Bun's generic 500.
export async function handleInterrupt(
  q: { interrupt(): Promise<void> } | undefined,
): Promise<RouteResult> {
  if (!q) {
    return { status: 409, body: { ok: false, error: "no active task" } };
  }
  try {
    await q.interrupt();
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    return {
      status: 409,
      body: { ok: false, error: `no active task (interrupt failed: ${message})` },
    };
  }
  return { status: 200, body: { ok: true } };
}
