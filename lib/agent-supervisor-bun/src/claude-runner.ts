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
      ...(pathToClaudeCodeExecutable ? { pathToClaudeCodeExecutable } : {}),
    },
  });

  for await (const event of stream) {
    onEvent(event);
  }
}
