import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createEventLogger, resumeSessionId } from "./event-log";

let dir: string;
let logPath: string;

beforeEach(() => {
  dir = mkdtempSync(join(tmpdir(), "cspace-event-log-"));
  logPath = join(dir, "events.ndjson");
});

afterEach(() => {
  rmSync(dir, { recursive: true, force: true });
});

function lines(path: string): string[] {
  return readFileSync(path, "utf8")
    .split("\n")
    .filter((l) => l.trim().length > 0);
}

function initEvent(sessionId: string): string {
  return JSON.stringify({
    ts: "2026-07-19T00:00:00.000Z",
    session: "primary",
    kind: "sdk-event",
    data: { type: "system", subtype: "init", session_id: sessionId },
  });
}

describe("createEventLogger", () => {
  test("appends NDJSON lines with ts, session, kind, data", () => {
    const log = createEventLogger({ path: logPath, session: "primary" });
    log("supervisor-start", { port: 6201 });
    log("user-turn", { text: "hello" });

    const got = lines(logPath).map((l) => JSON.parse(l));
    expect(got).toHaveLength(2);
    expect(got[0].kind).toBe("supervisor-start");
    expect(got[0].session).toBe("primary");
    expect(got[0].data).toEqual({ port: 6201 });
    expect(typeof got[0].ts).toBe("string");
    expect(got[1].kind).toBe("user-turn");
  });

  test("appends when the file does not exist yet (stat failure is non-fatal)", () => {
    // statSync throws ENOENT on first-ever write; the logger must swallow
    // that and create the file via append.
    const log = createEventLogger({ path: logPath, session: "s", maxBytes: 64 });
    log("first", null);
    expect(lines(logPath)).toHaveLength(1);
    expect(existsSync(`${logPath}.1`)).toBe(false);
  });

  test("does not rotate below maxBytes", () => {
    writeFileSync(logPath, "x".repeat(99));
    const log = createEventLogger({ path: logPath, session: "s", maxBytes: 100 });
    log("small", null);

    expect(existsSync(`${logPath}.1`)).toBe(false);
    const content = readFileSync(logPath, "utf8");
    expect(content.startsWith("x".repeat(99))).toBe(true);
    expect(content).toContain('"kind":"small"');
  });

  test("rotates at exactly maxBytes: current renamed to .1, append lands in fresh file", () => {
    writeFileSync(logPath, "y".repeat(100));
    const log = createEventLogger({ path: logPath, session: "s", maxBytes: 100 });
    log("after-rotate", { n: 1 });

    expect(readFileSync(`${logPath}.1`, "utf8")).toBe("y".repeat(100));
    const current = lines(logPath);
    expect(current).toHaveLength(1);
    expect(JSON.parse(current[0]).kind).toBe("after-rotate");
  });

  test("rotates when over maxBytes", () => {
    writeFileSync(logPath, "z".repeat(500));
    const log = createEventLogger({ path: logPath, session: "s", maxBytes: 100 });
    log("after-rotate", null);

    expect(readFileSync(`${logPath}.1`, "utf8")).toBe("z".repeat(500));
    expect(lines(logPath)).toHaveLength(1);
  });

  test("rotation clobbers a pre-existing .1", () => {
    writeFileSync(`${logPath}.1`, "OLD GENERATION");
    writeFileSync(logPath, "n".repeat(200));
    const log = createEventLogger({ path: logPath, session: "s", maxBytes: 100 });
    log("after-rotate", null);

    const rotated = readFileSync(`${logPath}.1`, "utf8");
    expect(rotated).toBe("n".repeat(200));
    expect(rotated).not.toContain("OLD GENERATION");
  });
});

describe("resumeSessionId", () => {
  test("returns undefined when the file does not exist", () => {
    expect(resumeSessionId(logPath)).toBeUndefined();
  });

  test("returns the last system/init session id", () => {
    writeFileSync(logPath, [initEvent("sess-a"), initEvent("sess-b")].join("\n") + "\n");
    expect(resumeSessionId(logPath)).toBe("sess-b");
  });

  test("skips malformed lines and non-init events", () => {
    const content = [
      "{not json at all",
      JSON.stringify({ kind: "user-turn", data: { text: "hi" } }),
      JSON.stringify({ kind: "sdk-event", data: { type: "assistant" } }),
      initEvent("sess-c"),
      "{\"kind\":\"sdk-event\",\"data\":{\"type\":\"system\",", // truncated mid-write
    ].join("\n");
    writeFileSync(logPath, content);
    expect(resumeSessionId(logPath)).toBe("sess-c");
  });

  test("ignores init events with empty session_id", () => {
    writeFileSync(
      logPath,
      JSON.stringify({
        kind: "sdk-event",
        data: { type: "system", subtype: "init", session_id: "" },
      }) + "\n",
    );
    expect(resumeSessionId(logPath)).toBeUndefined();
  });

  test("scans only the current file: an id lost to rotation degrades to fresh session", () => {
    // The init event lives in the rotated generation; the current file has
    // only post-rotation noise. Intended behavior: no resume.
    writeFileSync(`${logPath}.1`, initEvent("sess-rotated") + "\n");
    writeFileSync(
      logPath,
      JSON.stringify({ kind: "supervisor-start", data: { port: 6201 } }) + "\n",
    );
    expect(resumeSessionId(logPath)).toBeUndefined();
  });

  test("round-trips ids written by createEventLogger", () => {
    const log = createEventLogger({ path: logPath, session: "primary" });
    log("sdk-event", { type: "system", subtype: "init", session_id: "sess-live" });
    log("sdk-event", { type: "assistant" });
    expect(resumeSessionId(logPath)).toBe("sess-live");
  });
});
