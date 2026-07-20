import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { PromptStream } from "./prompt-stream";
import { runClaude } from "./claude-runner";
import { createEventLogger, resumeSessionId } from "./event-log";

const SESSION_ID = "primary";
const SESSIONS_DIR = "/sessions";
const WORKSPACE = "/workspace";
const CONTROL_PORT = Number(process.env.CSPACE_CONTROL_PORT ?? 6201);
const CONTROL_TOKEN = process.env.CSPACE_CONTROL_TOKEN ?? "";
const CLAUDE_PATH = process.env.CSPACE_CLAUDE_PATH ?? "/usr/local/bin/claude";

// Fail closed: an empty token would serve /send unauthenticated on 0.0.0.0
// (cs-finding 2026-07-16-supervisor-silent-death-modes-and-fail-open-auth).
// cmd_up always injects a token, so an empty one means broken provisioning —
// refuse to start rather than serve open. Must run BEFORE Bun.serve.
if (!CONTROL_TOKEN) {
  console.error(
    "cspace-supervisor: fatal: CSPACE_CONTROL_TOKEN is empty; refusing to serve unauthenticated on 0.0.0.0",
  );
  process.exit(1);
}

const sessionDir = join(SESSIONS_DIR, SESSION_ID);
mkdirSync(sessionDir, { recursive: true });
const eventLogPath = join(sessionDir, "events.ndjson");
const logEvent = createEventLogger({ path: eventLogPath, session: SESSION_ID });

const prompts = new PromptStream();

function fatalSdkError(err: unknown): never {
  logEvent("sdk-error", { error: String(err) });
  console.error(`cspace-supervisor: fatal: SDK stream died: ${String(err)}`);
  process.exit(1);
}

// Liveness contract (spec §3, cs-finding 2026-07-16-supervisor-silent-death-
// modes-and-fail-open-auth): a dead SDK stream must kill the process so the
// restart-loop wrapper respawns a live agent — never log-and-linger behind a
// healthy-looking /health. A rejection while resuming gets ONE retry with
// resume unset (fresh session) so a stale session id can't wedge every
// future restart. The retry deliberately does NOT re-scan events.ndjson —
// that could pick up the same (or another) poisoned id and re-resume.
async function runAgent(): Promise<void> {
  const resumeID = resumeSessionId(eventLogPath);
  if (resumeID) {
    logEvent("supervisor-resume", { sessionId: resumeID });
    console.log(`cspace-supervisor: resuming session ${resumeID}`);
  }
  try {
    await runClaude(
      prompts,
      WORKSPACE,
      (event) => {
        logEvent("sdk-event", event);
      },
      CLAUDE_PATH,
      resumeID,
    );
    return;
  } catch (err) {
    if (!resumeID) fatalSdkError(err);
    logEvent("resume-failed", { sessionId: resumeID, error: String(err) });
    console.error(
      `cspace-supervisor: resume of session ${resumeID} failed (${String(err)}); retrying with a fresh session`,
    );
  }
  try {
    await runClaude(
      prompts,
      WORKSPACE,
      (event) => {
        logEvent("sdk-event", event);
      },
      CLAUDE_PATH,
      undefined,
    );
  } catch (err) {
    fatalSdkError(err);
  }
}

runAgent().catch(fatalSdkError);

const server = Bun.serve({
  port: CONTROL_PORT,
  hostname: "0.0.0.0", // bind on all interfaces so sibling sandboxes can reach us via direct IP
  async fetch(req) {
    const url = new URL(req.url);

    const auth = req.headers.get("authorization") ?? "";
    if (auth !== `Bearer ${CONTROL_TOKEN}`) {
      return new Response("unauthorized", { status: 401 });
    }

    if (req.method === "POST" && url.pathname === "/send") {
      const body = (await req.json()) as { session?: string; text?: string };
      if (typeof body.text !== "string" || body.text.length === 0) {
        return Response.json({ error: "text required" }, { status: 400 });
      }
      if (body.session && body.session !== SESSION_ID) {
        return Response.json({ error: `unknown session ${body.session}` }, { status: 404 });
      }
      prompts.push(body.text);
      logEvent("user-turn", { source: "control-port", text: body.text });
      return Response.json({ ok: true });
    }

    if (req.method === "GET" && url.pathname === "/health") {
      return Response.json({ ok: true, session: SESSION_ID });
    }

    return new Response("not found", { status: 404 });
  },
});

logEvent("supervisor-start", { port: server.port, session: SESSION_ID });
console.log(`cspace-supervisor: listening on ${server.port}, session=${SESSION_ID}`);
