import { query, type Query, type SDKMessage, type SDKUserMessage } from "@anthropic-ai/claude-agent-sdk";
import type { PromptStream } from "./prompt-stream";

export type EventSink = (event: SDKMessage) => void;

// The SDK's query() takes `prompt: string | AsyncIterable<SDKUserMessage>`.
// PromptStream yields plain strings — this adapter wraps each string in the
// SDKUserMessage envelope the SDK expects.
async function* toUserMessages(
  prompts: AsyncIterable<string>,
): AsyncIterable<SDKUserMessage> {
  for await (const text of prompts) {
    yield {
      type: "user",
      session_id: "",
      parent_tool_use_id: null,
      message: {
        role: "user",
        content: [{ type: "text", text }],
      },
    };
  }
}

export async function runClaude(
  prompts: PromptStream,
  cwd: string,
  onEvent: EventSink,
  pathToClaudeCodeExecutable?: string,
  resumeSessionID?: string,
  role?: string,
  model?: string,
  // Invoked with the live SDK query handle BEFORE iteration starts, so a
  // caller (main.ts) can stash it for steering (POST /interrupt ->
  // handle.interrupt()). Fires once per runClaude() call, including each
  // fresh-session retry (run-agent.ts) — the caller is responsible for
  // replacing its stored handle on every invocation and clearing it when
  // that invocation's promise settles.
  onQuery?: (q: Query) => void,
): Promise<void> {
  // Append the role to Claude Code's own system prompt via the preset+append
  // form — NEVER replace it, so tool behavior and safety framing stay intact.
  // Built once and reused across the fresh-session retry (run-agent.ts), which
  // re-invokes this function with the same captured role/model.
  const systemPrompt = role
    ? ({ type: "preset", preset: "claude_code", append: role } as const)
    : undefined;

  const stream = query({
    prompt: toUserMessages(prompts),
    options: {
      cwd,
      // Sandboxes are unattended — there's no human to confirm tool calls.
      // bypassPermissions matches the existing Node supervisor's behavior
      // (lib/agent-supervisor/args.mjs) and is required for the agent to
      // actually USE its tools (Bash, Read, Write, Edit, etc.).
      permissionMode: "bypassPermissions",
      // Load project-level settings (CLAUDE.md, .claude/settings.json) from
      // cwd so the headless agent sees the same project instructions an
      // interactive session does. Deliberately always-on (spec §2): this is
      // the ONE query-option difference from the pre-config-surface baseline.
      // 'project' is the minimal source that pulls in /workspace/CLAUDE.md.
      settingSources: ["project"],
      // Role file (cspace up --role or committed .cspace/agent.md), resolved
      // by main.ts. Omitted entirely when no role is configured, keeping the
      // options byte-identical to the baseline aside from settingSources.
      ...(systemPrompt ? { systemPrompt } : {}),
      // Model from CSPACE_AGENT_MODEL (.cspace.json agent.model / --model).
      // Omitted when empty so the SDK/CLI default model is used.
      ...(model ? { model } : {}),
      // Resume a prior session when main.ts found one in events.ndjson.
      // Restart-loop respawn, fresh cspace up, and cspace down +
      // cspace up cycles all hit this path uniformly — the supervisor
      // is the single source of truth for "is there a session to resume".
      // The session JSONL itself is reachable because the host bind-mounts
      // ~/.claude/projects/-workspace/ into this sandbox at the same path
      // the SDK reads from.
      ...(resumeSessionID ? { resume: resumeSessionID } : {}),
      // Browser MCP servers — clients only. Chrome itself runs in a sidecar
      // container launched by cspace up; the MCP servers attach over CDP at
      // $CSPACE_BROWSER_CDP_URL. If the env var is unset, tool calls fail at
      // runtime — declined gracefully, doesn't crash the supervisor.
      //
      // Server names ("cspace-playwright", "cspace-chrome-devtools") and
      // flags match the cspace-browser Claude Code plugin
      // (lib/plugins/cspace-browser) so the headless and interactive paths
      // expose identical tool names to the agent. The Agent SDK's query()
      // does NOT auto-load CLI plugin MCP servers, so we register them here
      // explicitly — the plugin registration only takes effect when Claude
      // Code itself is driving the session interactively.
      mcpServers: {
        "cspace-playwright": {
          type: "stdio",
          command: "playwright-mcp",
          args: process.env.CSPACE_BROWSER_CDP_URL
            ? ["--isolated", "--cdp-endpoint", process.env.CSPACE_BROWSER_CDP_URL]
            : ["--isolated"],
        },
        "cspace-chrome-devtools": {
          type: "stdio",
          command: "chrome-devtools-mcp",
          args: process.env.CSPACE_BROWSER_CDP_URL
            ? ["--browserUrl", process.env.CSPACE_BROWSER_CDP_URL]
            : [],
        },
      },
      ...(pathToClaudeCodeExecutable ? { pathToClaudeCodeExecutable } : {}),
    },
  });

  onQuery?.(stream);

  for await (const event of stream) {
    onEvent(event);
  }
}
