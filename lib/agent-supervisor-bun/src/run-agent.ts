// Orchestrates a single supervisor lifecycle: run the SDK stream, retry once
// with a fresh session if a resumed session fails to start, and terminate
// the process on any terminal outcome so the restart-loop wrapper respawns
// a live agent. Extracted from main.ts so the safety-critical retry/exit
// contract (cs-finding 2026-07-16-supervisor-silent-death-modes-and-fail-
// open-auth) can be exercised with fake deps instead of a real SDK + process.
export interface RunAgentDeps {
  // Runs the SDK stream to completion; `resume` selects a session id to
  // resume, or undefined for a fresh session. Resolves when the stream ends
  // (which is itself abnormal — see the comment below), rejects if the SDK
  // call throws.
  runClaude: (resume?: string) => Promise<void>;
  // Session id to resume on the first attempt, if one was found in
  // events.ndjson at startup. undefined means "start fresh".
  resumeId?: string;
  log: (kind: string, data: unknown) => void;
  exit: (code: number) => never;
  // Drops the dead consumer's pending PromptStream waiter (without
  // resolving it) before handing the stream to the fresh-session retry's
  // new consumer. See prompt-stream.ts's detach().
  detach: () => void;
}

export async function runAgent(deps: RunAgentDeps): Promise<void> {
  const { runClaude, resumeId, log, exit, detach } = deps;

  try {
    await runClaude(resumeId);
  } catch (err) {
    if (!resumeId) {
      log("sdk-error", { error: String(err) });
      exit(1);
      return;
    }

    log("resume-failed", { sessionId: resumeId, error: String(err) });
    // The old consumer's PromptStream waiter belongs to the dead session;
    // detach it so it's dropped rather than accidentally resolved by a
    // /send that arrives for the fresh retry below.
    detach();

    try {
      // Deliberately literal `undefined` — never re-scan events.ndjson here.
      // Re-resolving to the same (or another) poisoned session id would
      // wedge every future restart.
      await runClaude(undefined);
    } catch (retryErr) {
      log("sdk-error", { error: String(retryErr) });
      exit(1);
      return;
    }

    // The retry resolved — still abnormal (see comment below).
    log("sdk-ended", {});
    exit(1);
    return;
  }

  // The PromptStream never closes during normal operation (main.ts's
  // instance lives for the process's whole life and is only ever pushed
  // to or detached, never closed) — so a runClaude() promise that RESOLVES
  // means the SDK stream ended on its own. That's abnormal: exit(1) so the
  // restart-loop wrapper respawns and resumes, rather than leaving this
  // process alive serving /health with no agent behind it.
  log("sdk-ended", {});
  exit(1);
}
