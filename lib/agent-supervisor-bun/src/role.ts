import { readFileSync } from "node:fs";

// The explicit override, written by `cspace up --role <host-path>` (the host
// resolves the file at up-time and writes its content here; the sessions dir
// is bind-mounted, so no env transport and no size limits).
const DEFAULT_OVERRIDE = "/sessions/agent-role.md";
// The committed convention: a role file checked into the workspace clone.
const DEFAULT_CONVENTION = "/workspace/.cspace/agent.md";

// resolveRole returns the agent role text to APPEND to the system prompt, or
// undefined when no role is configured. Resolution order (spec §2):
//   1. the explicit override (/sessions/agent-role.md)
//   2. the committed convention (/workspace/.cspace/agent.md)
//   3. none
//
// A missing/unreadable OR empty (whitespace-only) file counts as "no role at
// that layer" and falls through — so a stray empty agent-role.md neither sends
// a blank append to the SDK nor shadows a real committed convention file.
//
// Paths are injectable for tests; production calls it with no arguments.
export function resolveRole(paths?: {
  override?: string;
  convention?: string;
}): string | undefined {
  const override = paths?.override ?? DEFAULT_OVERRIDE;
  const convention = paths?.convention ?? DEFAULT_CONVENTION;
  return readRole(override) ?? readRole(convention);
}

function readRole(path: string): string | undefined {
  let content: string;
  try {
    content = readFileSync(path, "utf8");
  } catch {
    return undefined; // missing/unreadable → not configured at this layer
  }
  // Return the content verbatim (preserving formatting) when it carries a role;
  // treat whitespace-only as no role.
  return content.trim().length > 0 ? content : undefined;
}
