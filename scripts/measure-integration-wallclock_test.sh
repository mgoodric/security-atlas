#!/usr/bin/env bash
#
# measure-integration-wallclock_test.sh — slice 353 self-test for
# scripts/measure-integration-wallclock.sh.
#
# Uses RECORD mode (WALLCLOCK_SECONDS) throughout so the test is
# deterministic — it never times a real command and never asserts on a
# measured wall-clock sample (slice-381 lesson).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SCRIPT_DIR/measure-integration-wallclock.sh"

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
WM="$tmp/wallclock.tsv"

# --------------------------------------------------------------------
# Case 1 — record under trigger → status ok, appends a row.
# --------------------------------------------------------------------
set +e
out1="$(WALLCLOCK_FILE="$WM" WALLCLOCK_TRIGGER=1200 WALLCLOCK_SECONDS=900 WALLCLOCK_SHA=deadbee "$SCRIPT" 2>&1)"
rc1=$?
set -e
assert_eq "$rc1" "0" "case 1: under-trigger record → exit 0"
assert_contains "$out1" "status=ok" "case 1: status ok"
assert_contains "$out1" "~15 min" "case 1: human-readable minutes"
[[ -f "$WM" ]] && pass=$((pass+1)) || { fail=$((fail+1)); fail_messages+=("FAIL: case 1: watermark file created"); }
assert_contains "$(cat "$WM")" $'\t900\tok' "case 1: TSV row appended with seconds + status"

# --------------------------------------------------------------------
# Case 2 — record over trigger → OVER_TRIGGER warning, still exit 0.
# --------------------------------------------------------------------
set +e
out2="$(WALLCLOCK_FILE="$WM" WALLCLOCK_TRIGGER=1200 WALLCLOCK_SECONDS=1300 WALLCLOCK_SHA=cafe123 "$SCRIPT" 2>&1)"
rc2=$?
set -e
assert_eq "$rc2" "0" "case 2: over-trigger record STILL exits 0 (signal, not block)"
assert_contains "$out2" "OVER_TRIGGER" "case 2: status OVER_TRIGGER"
assert_contains "$out2" "TRIGGER CROSSED" "case 2: emits the action banner"
assert_contains "$out2" "Phase-A/B" "case 2: names the remediation slice"

# --------------------------------------------------------------------
# Case 3 — append-only: second record adds a row, keeps the first.
# --------------------------------------------------------------------
rows="$(grep -cE $'\t(ok|OVER_TRIGGER)$' "$WM")"
assert_eq "$rows" "2" "case 3: watermark is append-only (2 data rows after 2 records)"

# --------------------------------------------------------------------
# Case 4 — dry-run does not write.
# --------------------------------------------------------------------
WM2="$tmp/dry.tsv"
set +e
out4="$(WALLCLOCK_FILE="$WM2" WALLCLOCK_SECONDS=600 WALLCLOCK_DRY_RUN=true "$SCRIPT" 2>&1)"
rc4=$?
set -e
assert_eq "$rc4" "0" "case 4: dry-run exits 0"
[[ ! -f "$WM2" ]] && pass=$((pass+1)) || { fail=$((fail+1)); fail_messages+=("FAIL: case 4: dry-run wrote no file"); }

# --------------------------------------------------------------------
# Case 5 — bad trigger / bad seconds → exit 2.
# --------------------------------------------------------------------
set +e
out5="$(WALLCLOCK_FILE="$WM" WALLCLOCK_TRIGGER=notanumber WALLCLOCK_SECONDS=100 "$SCRIPT" 2>&1)"
rc5=$?
set -e
assert_eq "$rc5" "2" "case 5: non-integer trigger → exit 2"

set +e
out6="$(WALLCLOCK_FILE="$WM" WALLCLOCK_TRIGGER=1200 WALLCLOCK_SECONDS=abc "$SCRIPT" 2>&1)"
rc6=$?
set -e
assert_eq "$rc6" "2" "case 6: non-integer seconds → exit 2"

echo ""
echo "measure-integration-wallclock_test: $pass passed, $fail failed"
if (( fail > 0 )); then
  for msg in "${fail_messages[@]}"; do
    echo "  $msg"
  done
  exit 1
fi
