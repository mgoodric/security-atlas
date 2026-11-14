#!/usr/bin/env bash
#
# check-merge-gate-needs_test.sh — #1887 self-test for
# scripts/check-merge-gate-needs.sh.
#
# Synthetic workflows plus a real-tree assertion.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SCRIPT_DIR/check-merge-gate-needs.sh"
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

# run <workflow> [allowlist] sets out and rc.
run() {
  set +e
  out="$(CI_WORKFLOW="$1" MERGE_GATE_ALLOWLIST="${2:-}" bash "$SCRIPT" 2>&1)"
  rc=$?
  set -e
}

# write <file> <extra merge-gate needs line or empty>
write() {
  cat > "$1" <<EOF
name: CI
on: [pull_request]
jobs:
  changes:
    runs-on: ubuntu-latest
  build:
    needs: changes
    if: needs.changes.outputs.code == 'true'
    runs-on: ubuntu-latest
  lint-check:
    name: lint-check
    runs-on: ubuntu-latest
    steps:
      - run: "true"
  merge-gate:
    needs:
      - changes
      - build # Go · build
$2
    if: always()
    runs-on: ubuntu-latest
EOF
}

# Case 1 — every unconditional job is in needs → exit 0.
write "$tmp/ok.yml" "      - lint-check"
run "$tmp/ok.yml"
assert_eq "$rc" "0" "gated unconditional job passes"
assert_contains "$out" "OK" "pass prints OK"

# Case 2 — an unconditional job missing from needs → exit 1, named.
write "$tmp/gap.yml" ""
run "$tmp/gap.yml"
assert_eq "$rc" "1" "ungated unconditional job fails"
assert_contains "$out" "'lint-check' runs on every PR" "failure names the job"

# Case 3 — a conditional job missing from needs is not this check's concern.
cat > "$tmp/cond.yml" <<'EOF'
jobs:
  changes:
    runs-on: ubuntu-latest
  advisory:
    if: github.event_name == 'pull_request'
    runs-on: ubuntu-latest
  merge-gate:
    needs:
      - changes
    if: always()
EOF
run "$tmp/cond.yml"
assert_eq "$rc" "0" "conditional job outside needs passes"

# Case 4 — allowlisted with a reason → exit 0.
write "$tmp/allow.yml" ""
run "$tmp/allow.yml" "lint-check advisory until it has soaked"
assert_eq "$rc" "0" "allowlisted job passes"

# Case 5 — allowlisted without a reason → exit 1.
run "$tmp/allow.yml" "lint-check"
assert_eq "$rc" "1" "allowlist entry without a reason fails"
assert_contains "$out" "has no reason" "reasonless entry is named"

# Case 6 — stale allowlist entries (in needs; no such job) → exit 1.
run "$tmp/ok.yml" "lint-check no longer true"
assert_eq "$rc" "1" "allowlisted job already in needs fails"
assert_contains "$out" "stale ALLOWLIST entry 'lint-check'" "stale entry is named"
run "$tmp/ok.yml" "gone-job removed long ago"
assert_eq "$rc" "1" "allowlisted missing job fails"

# Case 7 — unrecognised layouts exit 2, never 0.
run "$tmp/does-not-exist.yml"
assert_eq "$rc" "2" "missing workflow exits 2"
printf 'jobs:\n  changes:\n    runs-on: ubuntu-latest\n' > "$tmp/nogate.yml"
run "$tmp/nogate.yml"
assert_eq "$rc" "2" "workflow without merge-gate exits 2"
printf 'jobs:\n  changes:\n    runs-on: x\n  merge-gate:\n    needs: [changes]\n' > "$tmp/flow.yml"
run "$tmp/flow.yml"
assert_eq "$rc" "2" "flow-style needs exits 2"

# Case 8 — the real ci.yml passes with the real allowlist.
set +e
out="$(cd "$REPO_ROOT" && bash "$SCRIPT" 2>&1)"
rc=$?
set -e
assert_eq "$rc" "0" "real ci.yml passes: $out"

for m in "${fail_messages[@]+"${fail_messages[@]}"}"; do
  echo "$m" >&2
done
echo "check-merge-gate-needs_test: $pass passed, $fail failed"
[[ $fail -eq 0 ]]
