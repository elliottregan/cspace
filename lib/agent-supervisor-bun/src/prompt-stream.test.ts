import { describe, expect, test } from "bun:test";
import { PromptStream } from "./prompt-stream";

describe("PromptStream", () => {
  test("yields pushed user turns in order", async () => {
    const ps = new PromptStream();
    ps.push("first");
    ps.push("second");
    ps.close();

    const seen: string[] = [];
    for await (const turn of ps) {
      seen.push(turn);
    }
    expect(seen).toEqual(["first", "second"]);
  });

  test("blocks until a turn is pushed", async () => {
    const ps = new PromptStream();
    const it = ps[Symbol.asyncIterator]();
    const pending = it.next();

    setTimeout(() => ps.push("delayed"), 10);
    const result = await pending;

    expect(result.done).toBe(false);
    expect(result.value).toBe("delayed");
    ps.close();
  });

  test("close resolves pending waiters with done", async () => {
    const ps = new PromptStream();
    const it = ps[Symbol.asyncIterator]();
    const pending = it.next();

    ps.close();
    const result = await pending;

    expect(result.done).toBe(true);
  });

  test("close drains queued turns before signalling done", async () => {
    const ps = new PromptStream();
    ps.push("queued");
    ps.close();

    const it = ps[Symbol.asyncIterator]();
    expect(await it.next()).toEqual({ value: "queued", done: false });
    expect((await it.next()).done).toBe(true);
  });

  test("push after close throws", () => {
    const ps = new PromptStream();
    ps.close();
    expect(() => ps.push("late")).toThrow("PromptStream is closed");
  });

  test("detach drops a dead consumer's waiter without resolving it; queued turns still reach the new consumer", async () => {
    const ps = new PromptStream();

    // Consumer A registers a waiter (queue is empty, so next() suspends).
    const itA = ps[Symbol.asyncIterator]();
    let aSettled = false;
    const pendingA = itA.next().then((r) => {
      aSettled = true;
      return r;
    });

    // The old session dies; main.ts detaches before handing the stream to
    // a fresh consumer.
    ps.detach();

    // Consumer B starts fresh and should receive newly pushed turns.
    const itB = ps[Symbol.asyncIterator]();
    const pendingB = itB.next();
    ps.push("delivered-to-b");
    const resultB = await pendingB;

    expect(resultB).toEqual({ value: "delivered-to-b", done: false });

    // Give the microtask queue a turn — A's promise must NOT have resolved.
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(aSettled).toBe(false);

    ps.close();
    void pendingA; // never resolves; left pending intentionally.
  });
});
