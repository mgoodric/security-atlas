#!/usr/bin/env bash
#
# slice-event.sh — append one slice state-transition event to _events.jsonl.
#
# The event log records states that git CANNOT prove (deferred, blocked on an open
# question, not-ready deploy-note, abandoned, not-a-code-bug). gen-status.sh overlays
# the LAST event per slice on top of the git-derived base. Appending is conflict-free
# across parallel worktrees — each agent writes its own line; git auto-merges distinct
# lines, unlike editing a shared markdown table.
#
# States that ARE git-derivable (merged / in-review / in-progress / ready) do not need
# events — merge a PR, open a PR, push a branch, or let it default. Events are for the
# rest. (You MAY still log them for an explicit audit trail; gen-status lets git win.)
#
#   ATLAS_EVENTS_FILE   event log path (default: <repo>/docs/issues/_events.jsonl)
#   ATLAS_NOW           timestamp      (default: today; pin for deterministic tests)
#
# Usage: bash scripts/slice-event.sh <slice> <state> [note] [ref]
#   e.g. just event 683 not-ready "edge migration-lag; blocked on maintainer access"
# Exit: 0 ok, 2 usage/validation error.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
EVENTS_FILE="${ATLAS_EVENTS_FILE:-$ROOT/docs/issues/_events.jsonl}"
NOW="${ATLAS_NOW:-$(date +%Y-%m-%d)}"

ALLOWED="ready in-progress in-review merged not-ready blocked deferred abandoned not-a-code-bug"

slice="${1:-}"; state="${2:-}"; note="${3:-}"; ref="${4:-}"

if [[ -z "$slice" || -z "$state" ]]; then
  echo "usage: slice-event.sh <slice> <state> [note] [ref]" >&2
  echo "states: $ALLOWED" >&2
  exit 2
fi
if ! [[ "$slice" =~ ^[0-9]+$ ]]; then
  echo "slice-event: slice must be numeric, got '$slice'" >&2
  exit 2
fi
case " $ALLOWED " in
  *" $state "*) ;;
  *) echo "slice-event: unknown state '$state'. allowed: $ALLOWED" >&2; exit 2 ;;
esac

jq -nc \
  --argjson slice "$slice" \
  --arg to "$state" \
  --arg ts "$NOW" \
  --arg ref "$ref" \
  --arg note "$note" \
  '{slice:$slice, to:$to, ts:$ts, ref:$ref, note:$note}' >> "$EVENTS_FILE"

echo "slice-event: appended {slice:$slice, to:$state} to $EVENTS_FILE"
