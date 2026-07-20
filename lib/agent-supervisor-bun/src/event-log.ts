import { appendFileSync, existsSync, readFileSync, renameSync, statSync } from "node:fs";

// 10 MiB — mirrors the daemon-log rotation pattern (single generation).
export const DEFAULT_MAX_BYTES = 10 * 1024 * 1024;

export type EventLogger = (kind: string, data: unknown) => void;

export interface EventLoggerOptions {
  // Path of the current-generation NDJSON file (events.ndjson).
  path: string;
  // Session label stamped on every line.
  session: string;
  // Rotation threshold; defaults to 10 MiB. Injectable so tests can use
  // tiny thresholds instead of writing 10 MiB fixtures.
  maxBytes?: number;
}

// createEventLogger returns an append-only NDJSON event logger with
// single-generation rotation: when the current file is at/over maxBytes it
// is renamed to `<path>.1` (clobbering any prior `.1`), then the new event
// is appended to a fresh file.
//
// Rotation is best-effort: if statSync throws (most commonly ENOENT on the
// first-ever write) or renameSync fails, we append anyway — losing a
// rotation beats losing the event, and an unbounded-but-alive log beats a
// crashed supervisor.
export function createEventLogger(opts: EventLoggerOptions): EventLogger {
  const { path, session } = opts;
  const maxBytes = opts.maxBytes ?? DEFAULT_MAX_BYTES;
  return (kind, data) => {
    try {
      if (statSync(path).size >= maxBytes) {
        renameSync(path, `${path}.1`);
      }
    } catch {
      // stat/rename failed (file missing, permissions, ...) — append anyway.
    }
    const line = JSON.stringify({
      ts: new Date().toISOString(),
      session,
      kind,
      data,
    });
    appendFileSync(path, line + "\n");
  };
}

// resumeSessionId returns the last SDK system/init session_id seen in
// events.ndjson, or undefined if events.ndjson doesn't exist or has none.
// Called at supervisor startup; lets us resume the conversation after a
// crash (the restart-loop wrapper respawns this binary; events.ndjson is
// on a host-bind-mount so it persists across restarts and even across
// cspace down + cspace up cycles).
//
// Scans ONLY the current file, never `<path>.1`: a resume id lost to
// rotation degrades to a fresh session — intended (spec §3.5).
//
// Permissive parser: malformed JSON lines are skipped rather than fatal,
// since events.ndjson is appended live and a partially-flushed final
// line is normal during graceful shutdown.
export function resumeSessionId(eventsPath: string): string | undefined {
  if (!existsSync(eventsPath)) return undefined;
  const lines = readFileSync(eventsPath, "utf8").split("\n");
  let last: string | undefined;
  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    try {
      const e = JSON.parse(trimmed);
      if (e.kind !== "sdk-event") continue;
      const d = e.data ?? {};
      if (
        d.type === "system" &&
        d.subtype === "init" &&
        typeof d.session_id === "string" &&
        d.session_id.length > 0
      ) {
        last = d.session_id;
      }
    } catch {
      // skip malformed lines
    }
  }
  return last;
}
