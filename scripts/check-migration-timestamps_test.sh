#!/usr/bin/env bash
#
# check-migration-timestamps_test.sh — #1880 self-test for
# scripts/check-migration-timestamps.sh.
#
# Synthetic fixtures plus a real-tree assertion.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SCRIPT_DIR/check-migration-timestamps.sh"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

pass=0
fail=0
fail_messages=()

assert_eq() {
  local got="$1" want="$2" msg="$3"
  if [[ "$got" == "$want" ]]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    fail_messages+=("FAIL: $msg (got '$got', want '$want')")
  fi
}

assert_contains() {
  local haystack="$1" needle="$2" msg="$3"
  if [[ "$haystack" == *"$needle"* ]]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    fail_messages+=("FAIL: $msg (output did not contain '$needle')")
  fi
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# run <dir> sets out and rc.
run() {
  set +e
  out="$(MIGRATIONS_DIR="$1" bash "$SCRIPT" 2>&1)"
  rc=$?
  set -e
}

# Case 1 — unique prefixes, each with an up and a down → exit 0.
mkdir "$tmp/unique"
touch "$tmp/unique/20260101000000_a.sql" "$tmp/unique/20260101000000_a.down.sql" \
  "$tmp/unique/20260102000000_b.sql"
run "$tmp/unique"
assert_eq "$rc" "0" "unique prefixes pass"

# Case 2 — two migrations on one prefix → exit 1, both named.
mkdir "$tmp/dup"
touch "$tmp/dup/20260101000000_a.sql" "$tmp/dup/20260101000000_b.sql" \
  "$tmp/dup/20260101000000_b.down.sql"
run "$tmp/dup"
assert_eq "$rc" "1" "shared prefix fails"
assert_contains "$out" "20260101000000_a" "failure names the first migration"
assert_contains "$out" "20260101000000_b" "failure names the second migration"

# Case 3 — the grandfathered pair alone → exit 0.
mkdir "$tmp/grand"
touch "$tmp/grand/20260811000000_aggregation_evaluation_meta_risk_pointer.sql" \
  "$tmp/grand/20260811000000_oauth_client_tenant_binding.sql"
run "$tmp/grand"
assert_eq "$rc" "0" "grandfathered pair passes"

# Case 4 — a new migration on the grandfathered prefix → exit 1.
cp -R "$tmp/grand" "$tmp/grand-plus"
touch "$tmp/grand-plus/20260811000000_new_thing.sql"
run "$tmp/grand-plus"
assert_eq "$rc" "1" "new migration on a grandfathered prefix fails"
assert_contains "$out" "20260811000000_new_thing" "failure names the new migration"

# Case 5 — missing directory → exit 2.
run "$tmp/nope"
assert_eq "$rc" "2" "missing directory is a misconfig"

# Case 6 — the real tree passes.
run "$REPO_ROOT/migrations/sql"
assert_eq "$rc" "0" "real migrations/sql passes: $out"

echo "check-migration-timestamps_test: $pass passed, $fail failed"
if [[ $fail -gt 0 ]]; then
  printf '%s\n' "${fail_messages[@]}" >&2
  exit 1
fi
