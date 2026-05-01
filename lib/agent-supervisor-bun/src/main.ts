import { mkdirSync, appendFileSync } from "node:fs";
import { join } from "node:path";
import { PromptStream } from "./prompt-stream";
import { runClaude } from "./claude-runner";

const SESSION_ID = "primary";
const SESSIONS_DIR = "/sessions";
const WORKSPACE = "/workspace";
const CONTROL_PORT = Number(process.env.CSPACE_CONTROL_PORT ?? 6201);
const CONTROL_TOKEN = process.env.CSPACE_CONTROL_TOKEN ?? "";
const CLAUDE_PATH = process.env.CSPACE_CLAUDE_PATH ?? "/usr/local/bin/claude";

const sessionDir = join(SESSIONS_DIR, SESSION_ID);
mkdirSync(sessionDir, { recursive: true });
const eventLog = join(sessionDir, "events.ndjson");

const prompts = new PromptStream();

function logEvent(kind: string, data: unknown): void {
  const line = JSON.stringify({
    ts: new Date().toISOString(),
    session: SESSION_ID,
    kind,
    data,
  });
  appendFileSync(eventLog, line + "\n");
}

runClaude(
  prompts,
  WORKSPACE,
  (event) => {
    logEvent("sdk-event", event);
  },
  CLAUDE_PATH,
).catch((err: unknown) => {
  logEvent("sdk-error", { error: String(err) });
});

const server = Bun.serve({
  port: CONTROL_PORT,
  hostname: "0.0.0.0", // bind on all interfaces so sibling sandboxes can reach us via direct IP
  async fetch(req) {
    const url = new URL(req.url);

    if (CONTROL_TOKEN) {
      const auth = req.headers.get("authorization") ?? "";
      if (auth !== `Bearer ${CONTROL_TOKEN}`) {
        return new Response("unauthorized", { status: 401 });
      }
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
