#!/usr/bin/env bash
#
# Smoke tests for scripts/check-integration-seed-order-independence.sh.
#
# Slice 461. The script's exit-0 / exit-1 paths require a live Postgres +
# applied migrations (they run the integration suite), so they are exercised
# by the integration JOB itself, not here. This offline self-test covers the
# environment-misconfiguration guards (exit 2) that need no DB:
#
#   1. DATABASE_URL unset      -> exit 2
#   2. DATABASE_URL_APP unset  -> exit 2
#
# Run: bash scripts/check-integration-seed-order-independence_test.sh
# Exits non-zero on first failed assertion.

set -eu

SCRIPT="$(cd "$(dirname "$0")" && pwd)/check-integration-seed-order-independence.sh"

if [[ ! -x "$SCRIPT" ]]; then
  echo "self-test: script not executable at $SCRIPT" >&2
  exit 2
fi

pass=0
fail=0
fail_messages=()

assert_exit() {
  local actual="$1" expected="$2" label="$3"
  if [[ "$actual" == "$expected" ]]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    fail_messages+=("$label: got exit '$actual', want exit '$expected'")
  fi
}

# Case 1: DATABASE_URL unset -> exit 2.
rc=0
env -u DATABASE_URL DATABASE_URL_APP="postgres://x" bash "$SCRIPT" >/dev/null 2>&1 || rc=$?
assert_exit "$rc" 2 "DATABASE_URL unset"

# Case 2: DATABASE_URL set, DATABASE_URL_APP unset -> exit 2.
rc=0
env -u DATABASE_URL_APP DATABASE_URL="postgres://x" bash "$SCRIPT" >/dev/null 2>&1 || rc=$?
assert_exit "$rc" 2 "DATABASE_URL_APP unset"

echo "check-integration-seed-order-independence_test: ${pass} passed, ${fail} failed"
if (( fail > 0 )); then
  for m in "${fail_messages[@]}"; do echo "  FAIL: $m" >&2; done
  exit 1
fi
