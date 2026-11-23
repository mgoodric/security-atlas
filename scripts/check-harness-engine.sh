#!/usr/bin/env bash
#
# check-harness-engine.sh — #3016
#
# Fail when an integration test builds the API server (api.New) without the
# authz engine. With no engine the router skips authzmw, so a test's role
# claim is never checked against a live user_roles grant. The handler gates
# then see a shape production never has, and a refusal test can pass for the
# wrong reason: #3008 found a harness whose 12 tests stayed green with the
# engine removed.
#
# A file passes when it:
#
#   - attaches an engine itself: liveroletest.Attach(, AttachAuthz( or a
#     package helper named attachAuthz(, or authz.NewEngine(; or
#   - opts out with a line comment naming why:
#       // harness-engine: none (<reason>)
#     for a test that is about the engine-less server itself; or
#   - is on the backlog (scripts/harness-engine-backlog.txt), the files that
#     had no engine when this guard landed.
#
# The backlog only shrinks. An entry fails the check when it goes stale: its
# file is gone, no longer calls api.New, or now attaches an engine. Delete
# the line in the PR that wires the engine.
#
# Scope: *_test.go files under internal/ and cmd/ that carry
# //go:build integration and call api.New(. A unit test has no database for
# the roles resolver, so it is out of scope.
#
# Findings:
#   NO-ENGINE   builds api.New with no engine, no opt-out, not on the backlog
#   STALE       a backlog entry that no longer needs to be there
#   BAD-OPTOUT  a harness-engine opt-out without a reason in parentheses
#
# Exit codes:
#   0  every in-scope file has an engine, an opt-out or a backlog entry
#   1  at least one finding
#   2  the backlog file is unreadable
#
# Usage:
#   bash scripts/check-harness-engine.sh
#   HARNESS_ENGINE_ROOT=<dir> HARNESS_ENGINE_BACKLOG=<file> bash scripts/check-harness-engine.sh
#
# Self-test:
#   bash scripts/check-harness-engine_test.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="${HARNESS_ENGINE_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
BACKLOG="${HARNESS_ENGINE_BACKLOG:-$SCRIPT_DIR/harness-engine-backlog.txt}"

API_NEW='(^|[^A-Za-z0-9_.])api\.New\('
ENGINE='liveroletest\.Attach\(|[aA]ttachAuthz\(|authz\.NewEngine\('
OPTOUT='^[[:space:]]*// harness-engine: none'
OPTOUT_REASON='^[[:space:]]*// harness-engine: none \([^)]+\)'

if [[ ! -r "$BACKLOG" ]]; then
  echo "check-harness-engine: backlog not readable: $BACKLOG" >&2
  exit 2
fi

cd "$ROOT"

# The backlog: one repo-relative path per line; blank lines and # comments
# are ignored.
backlog="$(grep -vE '^[[:space:]]*(#|$)' "$BACKLOG" || true)"

on_backlog() {
  grep -qxF -- "$1" <<<"$backlog"
}

in_scope() {
  local f="$1"
  grep -qE '^//go:build integration' "$f" && grep -qE "$API_NEW" "$f"
}

findings=0

files="$(find internal cmd -name '*_test.go' -type f 2>/dev/null | LC_ALL=C sort || true)"
while IFS= read -r f; do
  [[ -n "$f" ]] || continue
  in_scope "$f" || continue
  if grep -qE "$OPTOUT" "$f"; then
    if ! grep -qE "$OPTOUT_REASON" "$f"; then
      echo "BAD-OPTOUT  $f: write the reason as // harness-engine: none (<reason>)"
      findings=$((findings + 1))
    fi
    continue
  fi
  grep -qE "$ENGINE" "$f" && continue
  on_backlog "$f" && continue
  echo "NO-ENGINE   $f: builds api.New with no authz engine; call liveroletest.Attach before HTTPHandlerForTests"
  findings=$((findings + 1))
done <<<"$files"

while IFS= read -r f; do
  [[ -n "$f" ]] || continue
  if [[ ! -f "$f" ]]; then
    echo "STALE       $f: on the backlog but the file is gone; delete the line"
    findings=$((findings + 1))
  elif ! in_scope "$f"; then
    echo "STALE       $f: on the backlog but it no longer builds api.New in an integration test; delete the line"
    findings=$((findings + 1))
  elif grep -qE "$ENGINE" "$f"; then
    echo "STALE       $f: on the backlog but it attaches an engine now; delete the line"
    findings=$((findings + 1))
  fi
done <<<"$backlog"

if ((findings > 0)); then
  echo "check-harness-engine: $findings finding(s)" >&2
  exit 1
fi
echo "check-harness-engine: ok"
