import { query, type SDKMessage, type SDKUserMessage } from "@anthropic-ai/claude-agent-sdk";
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
): Promise<void> {
  const stream = query({
    prompt: toUserMessages(prompts),
    options: {
      cwd,
      // Sandboxes are unattended — there's no human to confirm tool calls.
      // bypassPermissions matches the existing Node supervisor's behavior
      // (lib/agent-supervisor/args.mjs) and is required for the agent to
      // actually USE its tools (Bash, Read, Write, Edit, etc.).
      permissionMode: "bypassPermissions",
      // Resume an existing session when cspace2-up detected one and
      // injected the session_id as env. Without this, every cspace2-up
      // boots a fresh session even when the host has prior conversation
      // state on disk. The session JSONL itself is reachable because the
      // host bind-mounts ~/.claude/projects/-workspace/ into this
      // sandbox at the same path the SDK reads from.
      ...(process.env.CSPACE_RESUME_SESSION_ID
        ? { resume: process.env.CSPACE_RESUME_SESSION_ID }
        : {}),
      // Browser MCP servers — clients only. Chrome itself runs in a sidecar
      // container launched by cspace2-up. The MCP servers attach over CDP at
      // $CSPACE_BROWSER_CDP_URL. If the env var is unset, the MCP servers
      // are still registered but their tool calls will fail at runtime —
      // declined gracefully, doesn't crash the supervisor.
      mcpServers: {
        playwright: {
          type: "stdio",
          command: "playwright-mcp",
          args: process.env.CSPACE_BROWSER_CDP_URL
            ? ["--cdp-endpoint", process.env.CSPACE_BROWSER_CDP_URL]
            : [],
        },
        "chrome-devtools": {
          type: "stdio",
          command: "chrome-devtools-mcp",
          args: process.env.CSPACE_BROWSER_CDP_URL
            ? ["--browser-url", process.env.CSPACE_BROWSER_CDP_URL]
            : [],
        },
      },
      ...(pathToClaudeCodeExecutable ? { pathToClaudeCodeExecutable } : {}),
    },
  });

  for await (const event of stream) {
    onEvent(event);
  }
}
