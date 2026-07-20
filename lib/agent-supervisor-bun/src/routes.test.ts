import { describe, expect, test } from "bun:test";
import { handleInterrupt } from "./routes";

describe("handleInterrupt", () => {
  test("undefined handle (no active task) -> 409", async () => {
    const result = await handleInterrupt(undefined);
    expect(result).toEqual({
      status: 409,
      body: { ok: false, error: "no active task" },
    });
  });

  test("resolving handle -> 200 ok", async () => {
    const result = await handleInterrupt({ interrupt: () => Promise.resolve() });
    expect(result).toEqual({ status: 200, body: { ok: true } });
  });

  test("rejecting handle (transport closed/dying) -> 409 with interrupt-failed message", async () => {
    const result = await handleInterrupt({
      interrupt: () => Promise.reject(new Error("transport closed")),
    });
    expect(result).toEqual({
      status: 409,
      body: { ok: false, error: "no active task (interrupt failed: transport closed)" },
    });
  });
});
