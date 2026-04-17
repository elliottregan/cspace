<!--
This directory holds project-shared Claude Code memory.

It is bind-mounted into every cspace container at:
  /home/dev/.claude/projects/-workspace/memory

Agents read and write here via the built-in memory system (four types:
user, feedback, project, reference). Committed to git so learnings
survive volume wipes and propagate to fresh clones.

See CLAUDE.md for the full convention.
-->
