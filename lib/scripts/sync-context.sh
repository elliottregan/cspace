#!/usr/bin/env bash
# sync-context.sh — Generate milestone context document from GitHub API.
#
# Usage:
#   ./sync-context.sh            # Write to docs/milestone-context.md
#   ./sync-context.sh --stdout   # Print to stdout (for prompt injection)
set -euo pipefail

REPO_ROOT="${PROJECT_ROOT:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
STDOUT_MODE=false

for arg in "$@"; do
  case "$arg" in
    --stdout) STDOUT_MODE=true ;;
  esac
done

if ! command -v gh >/dev/null 2>&1 || ! command -v jq >/dev/null 2>&1; then
  exit 0
fi

OWNER_REPO=$(gh repo view --json nameWithOwner --jq '.nameWithOwner' 2>/dev/null) || true
[ -z "$OWNER_REPO" ] && exit 0

MILESTONE_JSON=$(gh api "repos/${OWNER_REPO}/milestones?state=open&sort=created&direction=desc&per_page=1" 2>/dev/null || echo "[]")
MILESTONE_COUNT=$(echo "$MILESTONE_JSON" | jq 'length')
[ -z "$MILESTONE_COUNT" ] || [ "$MILESTONE_COUNT" = "null" ] || [ "$MILESTONE_COUNT" -eq 0 ] && exit 0

MILESTONE_TITLE=$(echo "$MILESTONE_JSON" | jq -r '.[0].title')
MILESTONE_NUMBER=$(echo "$MILESTONE_JSON" | jq -r '.[0].number')
MILESTONE_DESC=$(echo "$MILESTONE_JSON" | jq -r '.[0].description // ""')

GOALS=""
PRINCIPLES=""
if [ -n "$MILESTONE_DESC" ]; then
  GOALS=$(echo "$MILESTONE_DESC" | sed -n '/^## Goals/,/^## /{/^## Goals/d;/^## /d;p;}')
  [ -z "$GOALS" ] && GOALS=$(echo "$MILESTONE_DESC" | sed -n '/^## Goals/,$p' | sed '1d')
  PRINCIPLES=$(echo "$MILESTONE_DESC" | sed -n '/^## Architectural Principles/,/^## /{/^## Architectural Principles/d;/^## /d;p;}')
  [ -z "$PRINCIPLES" ] && PRINCIPLES=$(echo "$MILESTONE_DESC" | sed -n '/^## Architectural Principles/,$p' | sed '1d')
fi

ISSUES_JSON=$(gh api "repos/${OWNER_REPO}/issues?milestone=${MILESTONE_NUMBER}&state=all&per_page=100" 2>/dev/null || echo "[]")
ISSUE_COUNT=$(echo "$ISSUES_JSON" | jq 'length')
[ -z "$ISSUE_COUNT" ] || [ "$ISSUE_COUNT" = "null" ] || [ "$ISSUE_COUNT" -eq 0 ] && exit 0

ISSUES_PARSED=$(echo "$ISSUES_JSON" | jq '
  [.[] | select(.pull_request == null) | {
    number: .number,
    title: .title,
    state: .state,
    assignee: (.assignee.login // null),
    labels: [.labels[].name],
    blocked_by: (
      if .body then
        [.body | split("\n")[] |
          select(test("^\\s*blocked[- ]by:"; "i")) |
          [match("#([0-9]+)"; "g") | .captures[0].string | tonumber]
        ] | flatten
      else [] end
    ),
    blocks: (
      if .body then
        [.body | split("\n")[] |
          select(test("^\\s*blocks:"; "i")) |
          [match("#([0-9]+)"; "g") | .captures[0].string | tonumber]
        ] | flatten
      else [] end
    )
  }]
')

TMPFILE=$(mktemp)
trap 'rm -f "$TMPFILE"' EXIT

{
  echo "# Active Milestone: ${MILESTONE_TITLE}"
  echo ""
  [ -n "$GOALS" ] && { echo "## Goals"; echo "$GOALS"; echo ""; }
  [ -n "$PRINCIPLES" ] && { echo "## Architectural Principles"; echo "$PRINCIPLES"; echo ""; }
  echo "## Dependency Graph"
  echo ""
  echo "$ISSUES_PARSED" | jq -r '
    .[] |
    "#\(.number) \(.title)" +
    (if (.labels | length) > 0 then " [\(.labels | join(", "))]" else "" end) +
    "\n" +
    (if (.blocked_by | length) > 0 and (.blocks | length) > 0 then
      "  blocked-by: " + ([.blocked_by[] | "#\(.)"] | join(", ")) + "\n" +
      "  blocks: " + ([.blocks[] | "#\(.)"] | join(", "))
    elif (.blocked_by | length) > 0 then
      "  blocked-by: " + ([.blocked_by[] | "#\(.)"] | join(", "))
    elif (.blocks | length) > 0 then
      "  blocks: " + ([.blocks[] | "#\(.)"] | join(", "))
    else "  (independent)" end) + "\n"
  '
  echo "## Issue Status"
  echo ""
  echo "| Issue | Title | Status | Assignee | Labels |"
  echo "|-------|-------|--------|----------|--------|"
  echo "$ISSUES_PARSED" | jq -r '
    .[] |
    "| #\(.number) | \(.title) | \(.state) | \(.assignee // "—") | \(.labels | join(", ") | if . == "" then "—" else . end) |"
  '
  echo ""
} > "$TMPFILE"

if [ "$STDOUT_MODE" = true ]; then
  cat "$TMPFILE"
else
  OUTDIR="${REPO_ROOT}/docs"
  mkdir -p "$OUTDIR"
  cp "$TMPFILE" "${OUTDIR}/milestone-context.md"
  echo "Wrote ${OUTDIR}/milestone-context.md" >&2
fi
