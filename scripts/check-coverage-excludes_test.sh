#!/usr/bin/env bash
#
# check-coverage-excludes_test.sh — slice 353 self-test for
# scripts/check-coverage-excludes.sh.
#
# Synthetic positive + negative fixtures plus a real-tree assertion.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SCRIPT_DIR/check-coverage-excludes.sh"
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

# --------------------------------------------------------------------
# Case 1 — happy path: every exclude justified, no orphans → exit 0.
# --------------------------------------------------------------------
cat > "$tmp/good.json" <<'JSON'
{
  "excludes": ["a/", "b/"],
  "$exclude_justifications": {
    "$exclude_justifications_comment": "doc",
    "a/": "generated code",
    "b/": "integration-tested elsewhere"
  }
}
JSON
set +e
out1="$(COVERAGE_THRESHOLDS="$tmp/good.json" "$SCRIPT" 2>&1)"
rc1=$?
set -e
assert_eq "$rc1" "0" "case 1 (happy): all justified → exit 0"
assert_contains "$out1" "OK" "case 1: reports OK"

# --------------------------------------------------------------------
# Case 2 — missing justification → exit 1, names the prefix.
# --------------------------------------------------------------------
cat > "$tmp/missing.json" <<'JSON'
{
  "excludes": ["a/", "b/"],
  "$exclude_justifications": { "a/": "generated code" }
}
JSON
set +e
out2="$(COVERAGE_THRESHOLDS="$tmp/missing.json" "$SCRIPT" 2>&1)"
rc2=$?
set -e
assert_eq "$rc2" "1" "case 2 (missing): unjustified exclude → exit 1"
assert_contains "$out2" "b/" "case 2: names the unjustified prefix"

# --------------------------------------------------------------------
# Case 3 — empty justification string → exit 1.
# --------------------------------------------------------------------
cat > "$tmp/empty.json" <<'JSON'
{
  "excludes": ["a/"],
  "$exclude_justifications": { "a/": "" }
}
JSON
set +e
out3="$(COVERAGE_THRESHOLDS="$tmp/empty.json" "$SCRIPT" 2>&1)"
rc3=$?
set -e
assert_eq "$rc3" "1" "case 3 (empty): empty justification → exit 1"
assert_contains "$out3" "a/" "case 3: names the empty-justification prefix"

# --------------------------------------------------------------------
# Case 4 — orphan justification (no matching exclude) → exit 1.
# --------------------------------------------------------------------
cat > "$tmp/orphan.json" <<'JSON'
{
  "excludes": ["a/"],
  "$exclude_justifications": {
    "a/": "generated code",
    "gone/": "stale justification for a retired exclude"
  }
}
JSON
set +e
out4="$(COVERAGE_THRESHOLDS="$tmp/orphan.json" "$SCRIPT" 2>&1)"
rc4=$?
set -e
assert_eq "$rc4" "1" "case 4 (orphan): stale justification → exit 1"
assert_contains "$out4" "gone/" "case 4: names the orphan prefix"

# --------------------------------------------------------------------
# Case 5 — missing $exclude_justifications block entirely → exit 2.
# --------------------------------------------------------------------
cat > "$tmp/noblock.json" <<'JSON'
{ "excludes": ["a/"] }
JSON
set +e
out5="$(COVERAGE_THRESHOLDS="$tmp/noblock.json" "$SCRIPT" 2>&1)"
rc5=$?
set -e
assert_eq "$rc5" "2" "case 5 (no block): missing block → exit 2"

# --------------------------------------------------------------------
# Case 6 — malformed JSON → exit 2.
# --------------------------------------------------------------------
printf '{ not json' > "$tmp/bad.json"
set +e
out6="$(COVERAGE_THRESHOLDS="$tmp/bad.json" "$SCRIPT" 2>&1)"
rc6=$?
set -e
assert_eq "$rc6" "2" "case 6 (malformed): bad JSON → exit 2"

# --------------------------------------------------------------------
# Case 7 — REAL TREE: the guard passes against the actual repo file.
# --------------------------------------------------------------------
set +e
out7="$(COVERAGE_THRESHOLDS="$REPO_ROOT/cmd/scripts/coverage-thresholds.json" "$SCRIPT" 2>&1)"
rc7=$?
set -e
assert_eq "$rc7" "0" "case 7 (real tree): guard passes against the live thresholds file"
assert_contains "$out7" "OK" "case 7: real-tree run reports OK"

echo ""
echo "check-coverage-excludes_test: $pass passed, $fail failed"
if (( fail > 0 )); then
  for msg in "${fail_messages[@]}"; do
    echo "  $msg"
  done
  exit 1
fi
