#!/usr/bin/env bash
# Reads stream-json from stdin and prints one-line status updates.
# Used by `cspace issue` to show progress from claude --print.
set -euo pipefail

TURN=0
LAST_ACTIVITY=$(date +%s)
SESSION_ID=""
SESSION_FILE="/tmp/claude-session-id.txt"
RESULT_OK=true

while IFS= read -r line; do
    LAST_ACTIVITY=$(date +%s)

    type=$(echo "$line" | jq -r '.type // empty' 2>/dev/null) || continue

    # Capture session_id from the first event that carries one
    if [ -z "$SESSION_ID" ]; then
        sid=$(echo "$line" | jq -r '.session_id // empty' 2>/dev/null) || true
        if [ -n "$sid" ]; then
            SESSION_ID="$sid"
            printf '%s' "$sid" > "$SESSION_FILE"
            echo "  Session: $sid" >&2
        fi
    fi

    if [ "$type" = "assistant" ]; then
        TURN=$((TURN + 1))

        tools=$(echo "$line" | jq -r '
            [.message.content[]? | select(.type == "tool_use") | .name] | join(",")
        ' 2>/dev/null) || true

        text=$(echo "$line" | jq -r '
            [.message.content[]? | select(.type == "text") | .text] | join("") | .[0:120]
        ' 2>/dev/null) || true

        if [ -n "$tools" ]; then
            echo "$line" | jq -c '.message.content[]? | select(.type == "tool_use")' 2>/dev/null | while IFS= read -r tool_obj; do
                tool=$(echo "$tool_obj" | jq -r '.name // empty' 2>/dev/null)
                case "$tool" in
                    Bash)
                        cmd=$(echo "$tool_obj" | jq -r '.input.command // empty' 2>/dev/null | head -1 | cut -c1-80)
                        [ -n "$cmd" ] && echo "  [$TURN] -> $tool: $cmd" >&2
                        ;;
                    Read)
                        file=$(echo "$tool_obj" | jq -r '.input.file_path // empty' 2>/dev/null | sed 's|.*/||')
                        [ -n "$file" ] && echo "  [$TURN] -> Reading $file" >&2
                        ;;
                    Edit)
                        file=$(echo "$tool_obj" | jq -r '.input.file_path // empty' 2>/dev/null | sed 's|.*/||')
                        [ -n "$file" ] && echo "  [$TURN] -> Editing $file" >&2
                        ;;
                    Write)
                        file=$(echo "$tool_obj" | jq -r '.input.file_path // empty' 2>/dev/null | sed 's|.*/||')
                        [ -n "$file" ] && echo "  [$TURN] -> Writing $file" >&2
                        ;;
                    Glob|Grep)
                        pattern=$(echo "$tool_obj" | jq -r '.input.pattern // empty' 2>/dev/null)
                        [ -n "$pattern" ] && echo "  [$TURN] -> $tool: $pattern" >&2
                        ;;
                    Agent)
                        desc=$(echo "$tool_obj" | jq -r '.input.description // empty' 2>/dev/null)
                        [ -n "$desc" ] && echo "  [$TURN] -> Agent: $desc" >&2
                        ;;
                    Skill)
                        skill=$(echo "$tool_obj" | jq -r '.input.skill // empty' 2>/dev/null)
                        [ -n "$skill" ] && echo "  [$TURN] -> Skill: $skill" >&2
                        ;;
                    TaskCreate|TaskUpdate)
                        subj=$(echo "$tool_obj" | jq -r '.input.subject // .input.status // empty' 2>/dev/null)
                        [ -n "$subj" ] && echo "  [$TURN] -> $tool: $subj" >&2
                        ;;
                    *)
                        echo "  [$TURN] -> $tool" >&2
                        ;;
                esac
            done
        elif [ -n "$text" ]; then
            echo "  [$TURN] $text" >&2
        fi
    elif [ "$type" = "result" ]; then
        cost=$(echo "$line" | jq -r '.cost_usd // empty' 2>/dev/null)
        turns=$(echo "$line" | jq -r '.num_turns // empty' 2>/dev/null)
        subtype=$(echo "$line" | jq -r '.subtype // empty' 2>/dev/null)

        if [ -z "$SESSION_ID" ]; then
            sid=$(echo "$line" | jq -r '.session_id // empty' 2>/dev/null) || true
            if [ -n "$sid" ]; then
                SESSION_ID="$sid"
                printf '%s' "$sid" > "$SESSION_FILE"
            fi
        fi

        echo "" >&2
        if [ "$subtype" = "success" ]; then
            echo "Done ($turns turns, \$${cost})" >&2
        else
            RESULT_OK=false
            echo "FAILED — status: $subtype ($turns turns, \$${cost})" >&2
            result_text=$(echo "$line" | jq -r '.result // empty' 2>/dev/null)
            [ -n "$result_text" ] && echo "  $result_text" >&2
        fi
        echo "$line" | jq -r '.result // empty' 2>/dev/null
    fi
done

if [ "$RESULT_OK" = false ]; then
    exit 1
fi
