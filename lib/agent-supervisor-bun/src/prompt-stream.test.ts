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
});
