#!/usr/bin/env bash
# Verify nothing under search/ imports from cspace internals.
set -euo pipefail
if grep -rn "elliottregan/cspace/internal\|elliottregan/cspace/cmd" search/ 2>/dev/null; then
  echo "ERROR: search/ must not import from cspace internals." >&2
  exit 1
fi
echo "search/ dependency rule OK"
