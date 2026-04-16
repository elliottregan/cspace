#!/usr/bin/env bash
# Rebuild MEMORY.md from the frontmatter of every *.md file in the
# shared memory directory. Fires as a Claude Code PostToolUse hook
# (matcher: Write|Edit|MultiEdit) and as a SessionStart hook so the
# index stays correct under concurrent writes from sibling cspace
# containers — without racing on read-modify-write of MEMORY.md.
#
# Idempotent + lock-free: the rebuild is pure function of filesystem
# state. Two containers reconciling at the same moment produce identical
# MEMORY.md content; atomic tmp+rename ensures readers always see a
# self-consistent file. A lost-update window can only lose a line that
# the next reconcile will regenerate.
#
# Claude Code hook stdin is a JSON payload. For PostToolUse we peek at
# tool_input.file_path and exit quickly (no scan, no I/O) unless the
# write targeted the memory directory — so the hook is effectively
# free for the 99% of Write/Edit calls that edit project code rather
# than memory. SessionStart payloads have no file_path; we fall
# through to reconcile so a freshly-launched agent picks up sibling
# writes that landed while it was booting.

set -euo pipefail

MEMORY_DIR="${CLAUDE_MEMORY_DIR:-/home/dev/.claude/projects/-workspace/memory}"

# Capture stdin (small JSON payload). Done up-front so Claude Code
# doesn't EPIPE on us regardless of which exit path we take.
payload="$(cat 2>/dev/null || true)"

# Path filter: if the payload identifies a specific file (Write/Edit/
# MultiEdit) and that file is outside MEMORY_DIR, there's nothing for
# us to reconcile. Exit before scanning. When file_path is absent
# (SessionStart, or malformed payload) we fall through and reconcile
# — those are rare events where a full rebuild is the right behavior.
if [ -n "$payload" ] && command -v jq >/dev/null 2>&1; then
    file_path="$(printf '%s' "$payload" | jq -r '.tool_input.file_path // empty' 2>/dev/null || printf '')"
    if [ -n "$file_path" ]; then
        case "$file_path" in
            "$MEMORY_DIR"/*|"$MEMORY_DIR") ;;  # targets memory — reconcile
            *) exit 0 ;;                         # anything else — done
        esac
    fi
fi

# Fast no-op paths: missing dir, unreadable, or no entries.
if [ ! -d "$MEMORY_DIR" ] || [ ! -r "$MEMORY_DIR" ]; then
    exit 0
fi

index="$MEMORY_DIR/MEMORY.md"
tmp="$MEMORY_DIR/.MEMORY.md.tmp.$$"

# Render the index from all non-index *.md files in the dir.
# Output is sorted by filename for deterministic ordering across hosts.
{
    shopt -s nullglob
    for f in "$MEMORY_DIR"/*.md; do
        bn="$(basename "$f")"
        [ "$bn" = "MEMORY.md" ] && continue

        # Parse YAML-subset frontmatter. We only read the first frontmatter
        # block (between two `---` lines at the top of file). We pick up
        # `name` and `description` — enough to emit a usable index line.
        name=""
        desc=""
        in_fm=0
        closed=0
        # Use a subshell to avoid leaking state; read line-by-line.
        while IFS= read -r line || [ -n "$line" ]; do
            case "$line" in
                "---")
                    if [ "$in_fm" -eq 0 ]; then
                        in_fm=1
                    else
                        closed=1
                        break
                    fi
                    ;;
                "name: "*|"name:"*)
                    if [ "$in_fm" -eq 1 ]; then
                        name="${line#name:}"
                        name="${name# }"
                    fi
                    ;;
                "description: "*|"description:"*)
                    if [ "$in_fm" -eq 1 ]; then
                        desc="${line#description:}"
                        desc="${desc# }"
                    fi
                    ;;
            esac
        done < "$f"

        # Skip files without a complete frontmatter + name. Lets
        # non-memory markdown (drafts, notes) live in the dir harmlessly.
        [ "$closed" -eq 1 ] || continue
        [ -n "$name" ] || continue

        if [ -n "$desc" ]; then
            printf -- '- [%s](%s) — %s\n' "$name" "$bn" "$desc"
        else
            printf -- '- [%s](%s)\n' "$name" "$bn"
        fi
    done | LC_ALL=C sort
} > "$tmp"

# Atomic replace. If the rendered content matches what's already on
# disk, skip the rename to avoid needless mtime churn and inotify storms.
if [ -f "$index" ] && cmp -s "$tmp" "$index"; then
    rm -f "$tmp"
    exit 0
fi

mv "$tmp" "$index"
exit 0
