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
#     package helper named attachAuthz(, or authz.NewEngine(, in code (a
#     match inside a // or /* */ comment does not count); or
#   - opts out with a line comment naming why:
#       // harness-engine: none (<reason>)
#     for a test that is about the engine-less server itself; or
#   - is on the backlog (scripts/harness-engine-backlog.txt), the files that
#     had no engine when this guard landed.
#
# The backlog only shrinks. An entry fails the check when it goes stale: its
# file is gone, no longer calls api.New, or now attaches an engine. Delete
# the line in the PR that wires the engine. With HARNESS_ENGINE_BASE set to
# a git revision (CI passes the base commit), an entry that the backlog at
# that revision does not carry fails too: a new engine-less harness wires
# the engine or opts out, it does not join the backlog (#3026).
#
# Scope: *_test.go files under internal/ and cmd/ that carry
# //go:build integration and call api.New(. A unit test has no database for
# the roles resolver, so it is out of scope.
#
# Findings:
#   NO-ENGINE   builds api.New with no engine, no opt-out, not on the backlog
#   STALE       a backlog entry that no longer needs to be there
#   BAD-OPTOUT  a harness-engine opt-out without a reason in parentheses
#   GREW        a backlog entry the backlog at HARNESS_ENGINE_BASE lacks
#
# Exit codes:
#   0  every in-scope file has an engine, an opt-out or a backlog entry
#   1  at least one finding
#   2  the backlog file is unreadable, or HARNESS_ENGINE_BASE names no commit
#
# Usage:
#   bash scripts/check-harness-engine.sh
#   HARNESS_ENGINE_BASE=<rev> bash scripts/check-harness-engine.sh
#   HARNESS_ENGINE_ROOT=<dir> HARNESS_ENGINE_BACKLOG=<file> bash scripts/check-harness-engine.sh
#
# Self-test:
#   bash scripts/check-harness-engine_test.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="${HARNESS_ENGINE_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
BACKLOG="${HARNESS_ENGINE_BACKLOG:-$SCRIPT_DIR/harness-engine-backlog.txt}"
BASE="${HARNESS_ENGINE_BASE:-}"

API_NEW='(^|[^A-Za-z0-9_.])api\.New\('
ENGINE='liveroletest\.Attach\(|[aA]ttachAuthz\(|authz\.NewEngine\('
OPTOUT='^[[:space:]]*// harness-engine: none'
OPTOUT_REASON='^[[:space:]]*// harness-engine: none \([^)]+\)'

if [[ ! -r "$BACKLOG" ]]; then
  echo "check-harness-engine: backlog not readable: $BACKLOG" >&2
  exit 2
fi

# The backlog at BASE, read before the cd so a relative BACKLOG still
# resolves. A BASE that names no commit is exit 2, not a pass: a shrink-only
# check that silently skips proves nothing. A BASE that predates the backlog
# file has no entries, so every entry is GREW.
base_backlog=""
if [[ -n "$BASE" ]]; then
  backlog_dir="$(cd "$(dirname "$BACKLOG")" && pwd)"
  backlog_name="$(basename "$BACKLOG")"
  if ! git -C "$backlog_dir" rev-parse --verify --quiet "$BASE^{commit}" >/dev/null; then
    echo "check-harness-engine: HARNESS_ENGINE_BASE is not a commit here: $BASE" >&2
    exit 2
  fi
  base_backlog="$(git -C "$backlog_dir" show "$BASE:./$backlog_name" 2>/dev/null |
    grep -vE '^[[:space:]]*(#|$)' || true)"
fi

cd "$ROOT"

# The backlog: one repo-relative path per line; blank lines and # comments
# are ignored.
backlog="$(grep -vE '^[[:space:]]*(#|$)' "$BACKLOG" || true)"

on_backlog() {
  grep -qxF -- "$1" <<<"$backlog"
}

# code_only prints a Go file with its comments blanked, so an engine call
# named only in a comment does not count as one. It tracks "..." and `...`
# strings and '.' runes so a // inside a string is not read as a comment.
code_only() {
  awk '
    BEGIN { blk = 0; raw = 0; dq = "\""; sq = "\047"; bs = "\\" }
    {
      out = ""; n = length($0); i = 1; str = ""
      while (i <= n) {
        c = substr($0, i, 1); c2 = substr($0, i, 2)
        if (blk) {
          if (c2 == "*/") { blk = 0; i += 2 } else { i++ }
          continue
        }
        if (raw) {
          out = out c; if (c == "`") raw = 0; i++
          continue
        }
        if (str != "") {
          out = out c
          if (c == bs) { out = out substr($0, i + 1, 1); i += 2; continue }
          if (c == str) str = ""
          i++
          continue
        }
        if (c2 == "//") break
        if (c2 == "/*") { blk = 1; i += 2; continue }
        if (c == "`") raw = 1
        else if (c == dq || c == sq) str = c
        out = out c; i++
      }
      print out
    }
  ' "$1"
}

has_engine() {
  code_only "$1" | grep -E "$ENGINE" >/dev/null
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
  has_engine "$f" && continue
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
  elif has_engine "$f"; then
    echo "STALE       $f: on the backlog but it attaches an engine now; delete the line"
    findings=$((findings + 1))
  fi
  if [[ -n "$BASE" ]] && ! grep -qxF -- "$f" <<<"$base_backlog"; then
    echo "GREW        $f: on the backlog but not on it at $BASE; the backlog only shrinks, so wire the engine or opt out"
    findings=$((findings + 1))
  fi
done <<<"$backlog"

if ((findings > 0)); then
  echo "check-harness-engine: $findings finding(s)" >&2
  exit 1
fi
echo "check-harness-engine: ok"
