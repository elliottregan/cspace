import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { resolveRole } from "./role";

let dir: string;
let overridePath: string;
let conventionPath: string;
let missing: string;

beforeEach(() => {
  dir = mkdtempSync(join(tmpdir(), "cspace-role-"));
  overridePath = join(dir, "agent-role.md");
  conventionPath = join(dir, "agent.md");
  missing = join(dir, "does-not-exist.md");
});

afterEach(() => {
  rmSync(dir, { recursive: true, force: true });
});

describe("resolveRole", () => {
  test("override wins when both the override and convention files exist", () => {
    writeFileSync(overridePath, "OVERRIDE ROLE");
    writeFileSync(conventionPath, "CONVENTION ROLE");
    expect(resolveRole({ override: overridePath, convention: conventionPath })).toBe(
      "OVERRIDE ROLE",
    );
  });

  test("falls back to the convention when the override is absent", () => {
    writeFileSync(conventionPath, "CONVENTION ROLE");
    expect(resolveRole({ override: missing, convention: conventionPath })).toBe(
      "CONVENTION ROLE",
    );
  });

  test("returns undefined when neither file exists", () => {
    expect(resolveRole({ override: missing, convention: join(dir, "nope.md") })).toBeUndefined();
  });

  test("an empty override falls through to the convention (empty = no role at that layer)", () => {
    writeFileSync(overridePath, "   \n\t\n");
    writeFileSync(conventionPath, "CONVENTION ROLE");
    expect(resolveRole({ override: overridePath, convention: conventionPath })).toBe(
      "CONVENTION ROLE",
    );
  });

  test("an empty file with no convention resolves to undefined", () => {
    writeFileSync(overridePath, "");
    expect(resolveRole({ override: overridePath, convention: missing })).toBeUndefined();
  });

  test("preserves the file content verbatim (not trimmed)", () => {
    writeFileSync(overridePath, "line one\nline two\n");
    expect(resolveRole({ override: overridePath, convention: missing })).toBe(
      "line one\nline two\n",
    );
  });

  test("with no arguments it consults the default paths without throwing", () => {
    // /sessions/agent-role.md and /workspace/.cspace/agent.md do not exist on
    // the host test machine, so the default wiring must resolve to undefined
    // rather than throw.
    expect(resolveRole()).toBeUndefined();
  });
});
