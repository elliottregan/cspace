---
title: 'Project Context: Examples'
description: End-to-end walkthroughs showing what it is like to read and write the project brain from the coordinator's and implementer's perspectives.
sidebar:
  order: 5
---

These examples show the actual tool calls an agent makes against [Project Context](/architecture/project-context/) during a typical workflow. Each example is framed as the agent's reasoning (what it's about to do and why), the tool call it makes, the tool's response, and what the agent does with the result.

Tool calls are shown in the Python-function-call style the agent "sees" at inference time, not the wire JSON-RPC. They're equivalent.

## Example 1 — Coordinator: building a dispatch preamble

**Setup:** The coordinator has been asked to tackle issues `#42`, `#43`, and `#44` in parallel. Before launching the sub-agents, it fetches strategic context and bakes it into their starting prompts.

**Agent reasoning:**

> "I'm about to launch three implementers. They'll each need to know what the project is building and where it's heading, so they don't drift off-mission. Let me pull direction and roadmap."

**Tool call:**

```python
read_context(sections=["direction", "roadmap"])
```

**Result:**

```yaml
direction: |
  # Direction

  We're building cspace, a CLI that spins up isolated Claude Code
  devcontainers for autonomous agent workflows. The core value is
  "one click from a GitHub issue to a tested PR without a human in
  the loop." Every feature should make that loop faster or more reliable.

roadmap: |
  # Roadmap

  1. Shared project-context MCP server (in progress)
  2. Multi-agent coordination with dependency graphs (shipped)
  3. Reverse proxy for per-instance preview URLs (next)
  4. Incremental commit stream to surface partial progress (later)

decisions: null
discoveries: null
```

**What the coordinator does next:**

```bash
cat > /tmp/preamble-42.md <<EOF
## Project Context

$direction

$roadmap

_Call \`read_context\` with \`sections: ["decisions", "discoveries"]\` if your
task touches architecture or prior design choices._

---
EOF

# Substitute the preamble into the implementer playbook.
awk -v PRE=/tmp/preamble-42.md -v PH='${STRATEGIC_CONTEXT_PREAMBLE}' '
  BEGIN { while ((getline line < PRE) > 0) { p = p line ORS } close(PRE) }
  {
    out = ""
    while ((i = index($0, PH)) > 0) {
      out = out substr($0, 1, i - 1) p
      $0 = substr($0, i + length(PH))
    }
    print out $0
  }
' /opt/cspace/lib/agents/implementer.md | sed \
  -e "s|\${NUMBER}|42|g" \
  -e "s|\${BASE_BRANCH}|feature/login|g" \
  > /tmp/implementer-42.txt

cspace up issue-42 --base feature/login --prompt-file /tmp/implementer-42.txt
```

The implementer launches with the strategic preamble already in its first turn. No extra MCP call is needed to discover "what this project is."

---

## Example 2 — Implementer: reading context before touching code

**Setup:** An implementer agent has been dispatched against issue `#42`, which asks to "add retry logic to the webhook delivery pipeline."

**Agent reasoning:**

> "I've just read the issue. Before I start exploring the code, let me check whether this area has prior decisions or discoveries I should know about. If we already decided on a retry strategy elsewhere, I shouldn't reinvent it."

**Tool call:**

```python
read_context(
  sections=["decisions", "discoveries"],
  since="2026-01-01",
  limit=10
)
```

**Result:**

```yaml
decisions:
  - date: 2026-03-15
    slug: 2026-03-15-retry-policy-uses-exponential-backoff
    title: Retry policy uses exponential backoff with jitter
    sections:
      Context: |
        Initial retries caused thundering-herd spikes when a downstream
        service came back online. Customers with more events retried
        faster and drowned out smaller customers.
      Alternatives: |
        - Fixed interval (rejected: same thundering herd)
        - Token bucket per downstream (rejected: too much state)
      Decision: |
        Exponential backoff with full jitter, starting at 1s, capped at 5m.
        Max 6 attempts before dead-letter.
      Consequences: |
        Retry helper lives in internal/retry/. All outbound HTTP goes
        through it. Tests use a clock stub.

discoveries:
  - date: 2026-02-08
    slug: 2026-02-08-outbound-http-already-has-timeout-middleware
    title: Outbound HTTP already has timeout middleware
    sections:
      Finding: |
        internal/httpclient/ wraps every request with a 30s context
        deadline. Don't add per-call timeouts on top — they race with
        the middleware and produce confusing errors.
      Impact: |
        When adding new outbound calls, rely on the middleware, not
        manual ctx.WithTimeout wrappers.
```

**What the agent does next:**

> "Good. There's already a decision about retry policy — I should use `internal/retry/` instead of rolling my own backoff. And I shouldn't wrap a timeout around the retry loop because the HTTP middleware handles that. This saves me from at least one false-start."

The agent proceeds to implement by wrapping the webhook delivery call in the existing retry helper, rather than introducing a parallel retry abstraction.

---

## Example 3 — Implementer: logging a decision after shipping

**Setup:** The same implementer has just finished the feature and opened a PR. Before exiting, the playbook asks it to record anything load-bearing that a future agent might need.

**Agent reasoning:**

> "The PR hinges on a non-obvious choice: I used the existing retry helper but had to add a new `OnGiveUp` callback so the webhook pipeline can dead-letter. Future agents adding retries elsewhere should know this callback exists and why. That's worth a decision entry."

**Tool call:**

```python
log_decision(
  title="Webhook retry dead-letters via OnGiveUp callback",
  context=(
    "The existing internal/retry/ helper handles backoff but had no "
    "hook for terminal failure. The webhook pipeline needs to emit a "
    "dead-letter event after the final retry, before the error is "
    "returned to the caller."
  ),
  alternatives=(
    "- Return the error and let each caller dead-letter manually "
    "(rejected: every caller would repeat the same plumbing). "
    "- A global dead-letter queue (rejected: too coupled; not every "
    "retry is a webhook)."
  ),
  decision=(
    "Add an optional OnGiveUp(ctx, err) callback to internal/retry/. "
    "Called exactly once, after the final attempt fails, before the "
    "error returns. Webhook pipeline uses it to publish to the "
    "dead-letter topic."
  ),
  consequences=(
    "Callers that want dead-letter behavior opt in via the callback. "
    "Callers that just want the error get the old behavior. No "
    "breaking changes."
  )
)
```

**Result:**

```yaml
path: /workspace/.cspace/context/decisions/2026-04-14-webhook-retry-dead-letters-via-ongiveup-callback.md
```

**What the agent does next:**

> "Entry saved. I'll also log the discovery about the circular-import pitfall I hit while wiring this up — that one would trip up the next person."

---

## Example 4 — Implementer: logging a discovery

**Tool call:**

```python
log_discovery(
  title="Circular import between retry and httpclient",
  finding=(
    "internal/retry/ can't import internal/httpclient/ because "
    "httpclient already imports retry for its own reconnect logic. "
    "To pass the httpclient's default timeout to a retry caller, use "
    "the Options struct — don't reference httpclient from retry."
  ),
  impact=(
    "If a future change wants retry to know about HTTP specifics, "
    "either restructure via an interface in a third package or pass "
    "values through Options. Importing httpclient from retry will "
    "produce a compile error that takes a while to trace."
  )
)
```

**Result:**

```yaml
path: /workspace/.cspace/context/discoveries/2026-04-14-circular-import-between-retry-and-httpclient.md
```

The next implementer to work in `internal/retry/` will see this discovery in their pre-exploration `read_context` call and avoid the trap.

---

## Example 5 — Human curation: removing a stale entry

**Setup:** A maintainer is reviewing `.cspace/context/decisions/` and notices a 6-month-old entry that's been superseded by a more recent architectural shift. They want to remove it so it doesn't mislead future agents.

The usual path is just `rm .cspace/context/decisions/2025-10-11-foo.md` — the files are plain markdown under git. But the MCP tool is also available for programmatic curation passes:

**Tool call:**

```python
remove_entry(
  kind="decision",
  slug="2025-10-11-deprecated-event-routing-scheme"
)
```

**Result:**

```yaml
Tool result: "removed decision/2025-10-11-deprecated-event-routing-scheme"
```

**Invalid inputs are rejected before any filesystem access:**

```python
remove_entry(kind="decision", slug="../other-dir/foo")
# Tool error: invalid slug: "../other-dir/foo"
#             (must be non-empty and contain only [a-z0-9-])

remove_entry(kind="decision", slug="FOO")
# Tool error: invalid slug: "FOO"
#             (must be non-empty and contain only [a-z0-9-])
```

The charset guard keeps an AI caller (which can hallucinate plausible-looking paths) from reaching files outside the kind's subdirectory.

---

## Example 6 — First write on a fresh repo

**Setup:** Someone has just set up cspace on a new project. No `.cspace/context/` directory exists yet. An implementer starts a task and tries to read context.

**Tool call:**

```python
read_context()
```

**Result:**

```yaml
direction: ""
principles: ""
roadmap: ""
decisions: null
discoveries: null
```

Empty strings and empty arrays — no error, no files created on disk. `read_context` is deliberately side-effect-free so it's safe to call in pipelines that shouldn't produce writes (CI linting, dry-run tooling).

**The agent's first write seeds the templates:**

```python
log_decision(
  title="Initial decision example",
  context="...", alternatives="...", decision="...", consequences="..."
)
```

**After this call, the repo looks like:**

```
.cspace/context/
├── direction.md       ← seeded template with a comment placeholder
├── principles.md      ← seeded template with a comment placeholder
├── roadmap.md         ← seeded template with a comment placeholder
└── decisions/
    └── 2026-04-14-initial-decision-example.md
```

The seeded templates are markdown files with HTML comments explaining what goes in each section. A human can now edit them in place to fill in direction/principles/roadmap for the project. Subsequent `log_decision` or `log_discovery` calls will not overwrite them — the seeding check is "create only if missing."
