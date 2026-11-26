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

# Base-ref cases (#2883). A fixture repo whose "main" commit holds two
# migrations; the working tree then adds one more.
# run_base <dir> <ref> sets out and rc.
run_base() {
  set +e
  out="$(MIGRATIONS_DIR="$1" MIGRATIONS_BASE_REF="$2" bash "$SCRIPT" 2>&1)"
  rc=$?
  set -e
}

repo="$tmp/repo"
mkdir -p "$repo/sql"
git -C "$repo" init -q
touch "$repo/sql/20260101000000_a.sql" "$repo/sql/20260101000000_a.down.sql" \
  "$repo/sql/20260105000000_b.sql"
git -C "$repo" add sql
git -C "$repo" -c user.name=t -c user.email=t@example.invalid -c commit.gpgsign=false commit -qm base
base_sha="$(git -C "$repo" rev-parse HEAD)"

# Case 7 — nothing added → exit 0.
run_base "$repo/sql" "$base_sha"
assert_eq "$rc" "0" "no new migration passes against its base: $out"

# Case 8 — a new migration after the base's latest → exit 0.
touch "$repo/sql/20260106000000_c.sql" "$repo/sql/20260106000000_c.down.sql"
run_base "$repo/sql" "$base_sha"
assert_eq "$rc" "0" "new migration after the base's latest passes: $out"
rm "$repo/sql/20260106000000_c.sql" "$repo/sql/20260106000000_c.down.sql"

# Case 9 — a new migration equal to the base's latest, on a different name
# (the #2880 shape) → exit 1. The duplicate-prefix check fires too.
touch "$repo/sql/20260105000000_c.sql"
run_base "$repo/sql" "$base_sha"
assert_eq "$rc" "1" "new migration at the base's latest fails"
assert_contains "$out" "new migration 20260105000000_c is not after" "failure names the late migration"
rm "$repo/sql/20260105000000_c.sql"

# Case 10 — a new migration below the base's latest, prefix unused → exit 1.
# Only the base-ref check catches this one.
touch "$repo/sql/20260103000000_c.sql"
run "$repo/sql"
assert_eq "$rc" "0" "without a base ref, an unused earlier prefix passes"
run_base "$repo/sql" "$base_sha"
assert_eq "$rc" "1" "new migration below the base's latest fails"
assert_contains "$out" "20260105000000" "failure names the base's latest"
rm "$repo/sql/20260103000000_c.sql"

# Case 11 — an unreadable base ref → exit 2.
run_base "$repo/sql" "no-such-ref"
assert_eq "$rc" "2" "unreadable base ref is a misconfig"

echo "check-migration-timestamps_test: $pass passed, $fail failed"
if [[ $fail -gt 0 ]]; then
  printf '%s\n' "${fail_messages[@]}" >&2
  exit 1
fi
