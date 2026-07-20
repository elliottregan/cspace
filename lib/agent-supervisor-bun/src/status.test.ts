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

  test("a system/init event (session start, no turn in flight) -> idle", () => {
    expect(deriveState("system", "init")).toBe("idle");
  });

  test("a system/api_retry event (mid-turn API retry) -> working", () => {
    expect(deriveState("system", "api_retry")).toBe("working");
  });

  test("a system/compact_boundary event (mid-turn compaction) -> working", () => {
    expect(deriveState("system", "compact_boundary")).toBe("working");
  });

  test("a system event with no subtype recorded -> working", () => {
    expect(deriveState("system")).toBe("working");
  });

  test("any other/unknown event type -> working", () => {
    expect(deriveState("stream_event")).toBe("working");
  });
});
