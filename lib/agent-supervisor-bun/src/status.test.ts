import { describe, expect, test } from "bun:test";
import { deriveState } from "./status";

describe("deriveState", () => {
  test("undefined (no SDK event has landed yet) -> idle", () => {
    expect(deriveState(undefined)).toBe("idle");
  });

  test("a result-type event (turn complete) -> idle", () => {
    expect(deriveState("result")).toBe("idle");
  });

  test("an assistant event (mid-turn) -> working", () => {
    expect(deriveState("assistant")).toBe("working");
  });

  test("a user event (mid-turn) -> working", () => {
    expect(deriveState("user")).toBe("working");
  });

  test("a system event (init/status, not a terminal marker) -> working", () => {
    expect(deriveState("system")).toBe("working");
  });

  test("any other/unknown event type -> working", () => {
    expect(deriveState("stream_event")).toBe("working");
  });
});
