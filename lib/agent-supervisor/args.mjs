// Pure (SDK-free) helpers extracted from supervisor.mjs so that
// `node --test` can exercise them without needing node_modules/.
//
// Nothing here imports the Claude Agent SDK or any other dependency
// outside node's built-ins — keep it that way.

/**
 * Parse supervisor CLI arguments. Throws on unknown args, on `--help`
 * (message: `"__help__"`), and when `--resume-session` and
 * `--prompt-file` are both set.
 *
 * This is the canonical parser; the thin `parseArgs` wrapper in
 * supervisor.mjs handles help printing and `process.exit`.
 */
export function parseArgsForTest(argv) {
  const args = {
    role: 'agent',
    idleTimeoutMs: 10 * 60 * 1000,
    eventLogDir: '/logs/events',
    ignoreInboxBefore: 0,
  }
  for (let i = 2; i < argv.length; i++) {
    const arg = argv[i]
    if (arg === '--role') args.role = argv[++i]
    else if (arg === '--instance') args.instance = argv[++i]
    else if (arg === '--prompt-file') args.promptFile = argv[++i]
    else if (arg === '--resume-session') args.resumeSession = argv[++i]
    else if (arg === '--model') args.model = argv[++i]
    else if (arg === '--cwd') args.cwd = argv[++i]
    else if (arg === '--system-prompt-file') args.systemPromptFile = argv[++i]
    else if (arg === '--append-system-prompt') args.appendSystemPrompt = argv[++i]
    else if (arg === '--mcp-config') args.mcpConfig = argv[++i]
    else if (arg === '--idle-timeout-ms') args.idleTimeoutMs = Number.parseInt(argv[++i], 10)
    else if (arg === '--effort') args.effort = argv[++i]
    else if (arg === '--persistent') args.persistent = true
    else if (arg === '--event-log-dir') args.eventLogDir = argv[++i]
    else if (arg === '--ignore-inbox-before') args.ignoreInboxBefore = Number.parseInt(argv[++i], 10)
    else if (arg === '--help' || arg === '-h') {
      throw new Error('__help__')
    } else {
      throw new Error(`unknown argument ${arg}`)
    }
  }
  if (args.resumeSession && args.promptFile) {
    throw new Error('--resume-session and --prompt-file are mutually exclusive')
  }
  return args
}

/**
 * Build the options object passed to the Claude Agent SDK's query().
 * Pure function — no side effects, no I/O — so it's safely unit-testable.
 * When resumeSession is set, attaches `resume` and the SDK replays the
 * transcript instead of starting from a fresh prompt.
 */
export function buildQueryOptions({ model, effort, cwd, mcpServers, allowedTools, appendSystemPrompt, resumeSession }) {
  const options = {
    cwd,
    permissionMode: 'bypassPermissions',
    mcpServers,
    allowedTools,
    includePartialMessages: false,
  }
  if (model) {
    options.model = model
  }
  if (effort) {
    options.effort = effort
  }
  if (appendSystemPrompt) {
    options.appendSystemPrompt = appendSystemPrompt
  }
  if (resumeSession) {
    options.resume = resumeSession
  }
  return options
}
