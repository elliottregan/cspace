import { mkdirSync } from "node:fs";
import { join } from "node:path";
import type { Query } from "@anthropic-ai/claude-agent-sdk";
import { PromptStream } from "./prompt-stream";
import { runClaude } from "./claude-runner";
import { createEventLogger, resumeSessionId } from "./event-log";
import { resolveRole } from "./role";
import { runAgent } from "./run-agent";
import { handleInterrupt } from "./routes";
import { deriveState } from "./status";

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

// Steering state for GET /status (spec §4). currentQuery is the live SDK
// query handle — set by onQuery just before each runClaude() attempt starts
// iterating, cleared (via .finally() at the call site below) the moment
// that attempt's promise settles. This means the gap between a dead attempt
// and the fresh-session retry's own onQuery call is honestly represented as
// "no active task": POST /interrupt during that gap gets 409 rather than
// calling a stale handle. lastEventTs/lastEventType feed deriveState() and
// are surfaced as-is (undefined until the first SDK event lands).
let currentQuery: Query | undefined;
let lastEventTs: string | undefined;
let lastEventType: string | undefined;
let lastEventSubtype: string | undefined;

// Backstop for errors that escape runAgent() itself (e.g. logEvent()
// throwing mid-handler) rather than the SDK call it supervises. runAgent's
// own log/exit contract should make this unreachable in practice; if even
// this throws, Bun's unhandled-rejection default still exits non-zero, so
// the restart-loop wrapper respawns either way.
function fatalSdkError(err: unknown): never {
  console.error(`cspace-supervisor: fatal: unexpected supervisor error: ${String(err)}`);
  process.exit(1);
}

// console-visible companion to logEvent — see run-agent.ts for the actual
// liveness contract (spec §3, cs-finding 2026-07-16-supervisor-silent-death-
// modes-and-fail-open-auth) this drives.
function log(kind: string, data: unknown): void {
  logEvent(kind, data);
  if (kind === "resume-failed") {
    const { sessionId, error } = data as { sessionId: string; error: string };
    console.error(
      `cspace-supervisor: resume of session ${sessionId} failed (${error}); retrying with a fresh session`,
    );
  } else if (kind === "sdk-error") {
    const { error } = data as { error: string };
    console.error(`cspace-supervisor: fatal: SDK stream died: ${error}`);
  } else if (kind === "sdk-ended") {
    console.error("cspace-supervisor: fatal: SDK stream ended without error (unexpected); exiting to respawn");
  }
}

const resumeID = resumeSessionId(eventLogPath);
if (resumeID) {
  logEvent("supervisor-resume", { sessionId: resumeID });
  console.log(`cspace-supervisor: resuming session ${resumeID}`);
}

// Agent config surface (spec §2). Role: appended to the system prompt, never
// replaces it. Model: the SDK `model` option when non-empty. Both are captured
// here and threaded through runAgent's closure so they also reach the
// fresh-session retry (run-agent.ts).
const role = resolveRole();
if (role) {
  logEvent("agent-role", { bytes: role.length });
  console.log(`cspace-supervisor: agent role loaded (${role.length} bytes)`);
}
const model = process.env.CSPACE_AGENT_MODEL || undefined;
if (model) {
  logEvent("agent-model", { model });
  console.log(`cspace-supervisor: agent model = ${model}`);
}

runAgent({
  runClaude: (resume) =>
    runClaude(
      prompts,
      WORKSPACE,
      (event) => {
        logEvent("sdk-event", event);
        lastEventTs = new Date().toISOString();
        lastEventType = event.type;
        lastEventSubtype = "subtype" in event ? event.subtype : undefined;
      },
      CLAUDE_PATH,
      resume,
      role,
      model,
      (q) => {
        currentQuery = q;
      },
    ).finally(() => {
      currentQuery = undefined;
    }),
  resumeId: resumeID,
  log,
  exit: process.exit,
  detach: () => prompts.detach(),
}).catch(fatalSdkError);

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

    if (req.method === "POST" && url.pathname === "/interrupt") {
      const result = await handleInterrupt(currentQuery);
      if (result.body.ok) {
        logEvent("interrupt", { source: "control-port" });
      }
      return Response.json(result.body, { status: result.status });
    }

    if (req.method === "GET" && url.pathname === "/status") {
      return Response.json({
        ok: true,
        session: SESSION_ID,
        state: deriveState(lastEventType, lastEventSubtype),
        lastEventTs,
        lastEventType,
        lastEventSubtype,
        queueDepth: prompts.depth(),
      });
    }

    return new Response("not found", { status: 404 });
  },
});

logEvent("supervisor-start", { port: server.port, session: SESSION_ID });
console.log(`cspace-supervisor: listening on ${server.port}, session=${SESSION_ID}`);
