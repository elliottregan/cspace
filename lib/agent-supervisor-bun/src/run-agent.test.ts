import { describe, expect, test } from "bun:test";
import { runAgent, type RunAgentDeps } from "./run-agent";

// Marker thrown by the fake exit() so we can assert control flow stopped
// exactly where the implementation calls exit(), without actually killing
// the test process the way process.exit would.
class FakeExit extends Error {
  constructor(public code: number) {
    super(`exit(${code})`);
  }
}

function makeDeps(overrides: Partial<RunAgentDeps> & { runClaude: RunAgentDeps["runClaude"] }): {
  deps: RunAgentDeps;
  logs: Array<{ kind: string; data: unknown }>;
  exitCodes: number[];
  detachCalls: number;
} {
  const logs: Array<{ kind: string; data: unknown }> = [];
  const exitCodes: number[] = [];
  let detachCalls = 0;

  const deps: RunAgentDeps = {
    runClaude: overrides.runClaude,
    resumeId: overrides.resumeId,
    log: (kind, data) => {
      logs.push({ kind, data });
    },
    exit: (code: number) => {
      exitCodes.push(code);
      throw new FakeExit(code);
    },
    detach: () => {
      detachCalls++;
    },
  };

  return { deps, logs, exitCodes, detachCalls };
}

describe("runAgent", () => {
  test("(a) reject, no resume id -> logs sdk-error and exits 1", async () => {
    const result = makeDeps({
      runClaude: async () => {
        throw new Error("boom");
      },
      resumeId: undefined,
    });

    await expect(runAgent(result.deps)).rejects.toBeInstanceOf(FakeExit);

    expect(result.logs).toEqual([{ kind: "sdk-error", data: { error: "Error: boom" } }]);
    expect(result.exitCodes).toEqual([1]);
  });

  test("(b) reject with resume id -> logs resume-failed, detaches, retries fresh", async () => {
    const calls: Array<string | undefined> = [];
    const result = makeDeps({
      runClaude: async (resume) => {
        calls.push(resume);
        if (calls.length === 1) throw new Error("resume broke");
        // retry resolves cleanly for this test.
      },
      resumeId: "sess-123",
    });

    await expect(runAgent(result.deps)).rejects.toBeInstanceOf(FakeExit);

    expect(calls).toEqual(["sess-123", undefined]);
    expect(result.logs[0]).toEqual({
      kind: "resume-failed",
      data: { sessionId: "sess-123", error: "Error: resume broke" },
    });
  });

  test("(c) resume fails, retry also rejects -> logs sdk-error and exits 1", async () => {
    const calls: Array<string | undefined> = [];
    const result = makeDeps({
      runClaude: async (resume) => {
        calls.push(resume);
        throw new Error(`fail-${calls.length}`);
      },
      resumeId: "sess-abc",
    });

    await expect(runAgent(result.deps)).rejects.toBeInstanceOf(FakeExit);

    expect(calls).toEqual(["sess-abc", undefined]);
    expect(result.logs.map((l) => l.kind)).toEqual(["resume-failed", "sdk-error"]);
    expect(result.logs[1]).toEqual({ kind: "sdk-error", data: { error: "Error: fail-2" } });
    expect(result.exitCodes).toEqual([1]);
  });

  test("(d) runClaude resolves -> logs sdk-ended and exits 1 (zombie-on-resolve)", async () => {
    const result = makeDeps({
      runClaude: async () => {
        // resolves cleanly — abnormal stream end.
      },
      resumeId: undefined,
    });

    await expect(runAgent(result.deps)).rejects.toBeInstanceOf(FakeExit);

    expect(result.logs).toEqual([{ kind: "sdk-ended", data: {} }]);
    expect(result.exitCodes).toEqual([1]);
  });

  test("(e) resume retry passes undefined, not the stale resume id", async () => {
    const calls: Array<string | undefined> = [];
    const result = makeDeps({
      runClaude: async (resume) => {
        calls.push(resume);
        if (calls.length === 1) throw new Error("stale session dead");
      },
      resumeId: "stale-session",
    });

    await expect(runAgent(result.deps)).rejects.toBeInstanceOf(FakeExit);

    expect(calls[0]).toBe("stale-session");
    expect(calls[1]).toBeUndefined();
  });

  test("detach is called before the retry, not after", async () => {
    const order: string[] = [];
    const result = makeDeps({
      runClaude: async (resume) => {
        order.push(resume === undefined ? "retry" : "first");
        if (resume !== undefined) throw new Error("dead");
      },
      resumeId: "sess-xyz",
    });
    const deps: RunAgentDeps = {
      ...result.deps,
      detach: () => {
        order.push("detach");
      },
    };

    await expect(runAgent(deps)).rejects.toBeInstanceOf(FakeExit);

    expect(order).toEqual(["first", "detach", "retry"]);
  });
});
