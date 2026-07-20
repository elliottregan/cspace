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
});
