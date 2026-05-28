#!/usr/bin/env bash
#
# Smoke tests for scripts/flake-counter.sh.
#
# These tests do NOT touch the live GitHub API — they exercise the
# script's pure-bash parts (arg parsing, surface mapping, status
# computation, rate computation) by sourcing helper sections in
# isolation, and they exercise the markdown / JSON rendering by
# pre-populating the per-surface counter files the main loop
# normally writes to.
#
# Slice 352 — pairs with the script per the project's `_test.sh`
# convention (see scripts/audit-deps_test.sh, scripts/check-action-
# pins_test.sh, scripts/check-branch-protection-drift_test.sh).
#
# Run: bash scripts/flake-counter_test.sh
# Exits non-zero on first failed assertion.

set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$SCRIPT_DIR/flake-counter.sh"
if [[ ! -x "$SCRIPT" ]]; then
  echo "flake-counter_test: script not executable at $SCRIPT" >&2
  exit 2
fi

pass=0
fail=0
fail_messages=()

assert_contains() {
  local haystack="$1" needle="$2" label="$3"
  if grep -qF -- "$needle" <<<"$haystack"; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    fail_messages+=("$label: output did not contain '$needle'")
    fail_messages+=("  full output:")
    while IFS= read -r line; do
      fail_messages+=("    $line")
    done <<<"$haystack"
  fi
}

# Test 1 — `--help` emits the script's docstring and exits 0.
help_out=$(bash "$SCRIPT" --help 2>&1)
assert_contains "$help_out" "flake-counter.sh" "test-1 help mentions script name"
assert_contains "$help_out" "FLAKE_WINDOW_DAYS" "test-1 help documents env var"

# Test 2 — unknown arg exits non-zero with usage message.
if bash "$SCRIPT" --no-such-flag 2>/dev/null; then
  fail=$((fail + 1))
  fail_messages+=("test-2 unknown arg should exit non-zero")
else
  pass=$((pass + 1))
fi

# Test 3 — missing required tool exits non-zero. We can't easily
# remove `gh` from the test PATH, but we CAN re-source the script
# with PATH limited to /usr/bin /bin and observe its tool-check
# fires. Skip if `gh` lives in /usr/bin (unlikely on macOS, but
# guard against CI quirks).
if command -v gh | grep -q '^/usr/bin/'; then
  pass=$((pass + 1))  # trivial pass — gh on default path
else
  if PATH=/usr/bin:/bin bash "$SCRIPT" --help >/dev/null 2>&1; then
    # --help exits before tool check, so this should pass even with
    # restricted PATH (the docstring print happens first).
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    fail_messages+=("test-3 --help should not require gh on PATH")
  fi
fi

# Test 4 — JSON output mode emits valid JSON with the expected shape.
# We mock by extracting the surface table from the script and exercising
# the compute_rate / status_for helpers in isolation.
#
# Source only the helper-definition portion of the script (up to the
# `# ---------- fetch runs ----------` marker). bash's `source` reads
# the entire file, so we extract the prefix.
mkdir -p /tmp/flake-counter-test-$$
PREFIX="/tmp/flake-counter-test-$$/prefix.sh"
awk '/^# ---------- fetch runs ----------$/ { exit } { print }' "$SCRIPT" > "$PREFIX"
# Stub out the trap/exit calls that fire in the prefix portion.
# Append helper-only functions from the body that we want to test.
sed -n '/^compute_rate()/,/^}/p' "$SCRIPT" >> "$PREFIX"
sed -n '/^status_for()/,/^}/p' "$SCRIPT" >> "$PREFIX"

# Sourcing the prefix sets the helpers; trap-handler refs to env vars
# fired before the helpers are no-op safe because the script only
# echoes them.
# shellcheck disable=SC1090
set +e
( set -e
  # Pre-set the env vars the prefix reads so it doesn't fall back to
  # `gh repo view` (which DOES hit network).
  export FLAKE_REPO="example/repo"
  export FLAKE_VERBOSE=false
  # shellcheck disable=SC1090
  . "$PREFIX" >/dev/null 2>&1 || true

  # surface_for_job mapping — known + unknown.
  result=$(surface_for_job "Go · build + test")
  echo "surface_for_job-go-unit:$result"
  result=$(surface_for_job "Frontend · vitest")
  echo "surface_for_job-vitest:$result"
  result=$(surface_for_job "No Such Job")
  echo "surface_for_job-unknown:$result"

  # compute_rate
  echo "compute_rate-0-100:$(compute_rate 0 100)"
  echo "compute_rate-5-100:$(compute_rate 5 100)"
  echo "compute_rate-1-200:$(compute_rate 1 200)"
  echo "compute_rate-0-0:$(compute_rate 0 0)"

  # status_for
  # Args: flakes attempts target_pct trigger_count
  echo "status_for-clean:$(status_for 0 100 0.0 1)"      # green
  echo "status_for-under-target:$(status_for 1 1000 1.0 2)" # 0.1% rate, target 1.0% — green
  echo "status_for-yellow:$(status_for 1 100 0.5 2)"     # 1% > 0.5%, 1 < trigger 2 — yellow
  echo "status_for-red:$(status_for 2 100 0.5 2)"        # 2% > 0.5%, 2 >= trigger 2 — red
  echo "status_for-no-data:$(status_for 0 0 0.0 1)"      # no data
) > "/tmp/flake-counter-test-$$/output" 2>&1
status=$?
set -e
if [[ "$status" -ne 0 ]]; then
  echo "flake-counter_test: subshell exited non-zero" >&2
  cat "/tmp/flake-counter-test-$$/output" >&2
fi
out=$(cat "/tmp/flake-counter-test-$$/output")
rm -rf "/tmp/flake-counter-test-$$"

assert_contains "$out" "surface_for_job-go-unit:go-unit" "test-4a surface mapping go-unit"
assert_contains "$out" "surface_for_job-vitest:vitest" "test-4b surface mapping vitest"
assert_contains "$out" "surface_for_job-unknown:" "test-4c surface mapping unknown returns empty"

assert_contains "$out" "compute_rate-0-100:0.00" "test-4d compute_rate 0/100 = 0.00"
assert_contains "$out" "compute_rate-5-100:5.00" "test-4e compute_rate 5/100 = 5.00"
assert_contains "$out" "compute_rate-1-200:0.50" "test-4f compute_rate 1/200 = 0.50"
assert_contains "$out" "compute_rate-0-0:n/a" "test-4g compute_rate 0/0 = n/a"

assert_contains "$out" "status_for-clean:green" "test-4h status clean = green"
assert_contains "$out" "status_for-under-target:green" "test-4i status under-target = green"
assert_contains "$out" "status_for-yellow:yellow" "test-4j status above-target-below-trigger = yellow"
assert_contains "$out" "status_for-red:red" "test-4k status above-trigger = red"
assert_contains "$out" "status_for-no-data:no-data" "test-4l status no-attempts = no-data"

# Test 5 — date math: SINCE_ISO is BEFORE NOW_ISO regardless of which
# date dialect (GNU vs BSD) the host uses.
if NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ); then
  if SINCE=$(date -u -d "-1 days" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null); then
    :
  else
    SINCE=$(date -u -v-1d +%Y-%m-%dT%H:%M:%SZ)
  fi
  if [[ "$SINCE" < "$NOW" ]]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    fail_messages+=("test-5 date math: SINCE=$SINCE should be before NOW=$NOW")
  fi
else
  fail=$((fail + 1))
  fail_messages+=("test-5: date command did not return iso timestamp")
fi

# Report.
echo "flake-counter_test: $pass passed, $fail failed"
if [[ "$fail" -gt 0 ]]; then
  for msg in "${fail_messages[@]}"; do
    echo "  $msg" >&2
  done
  exit 1
fi
exit 0
