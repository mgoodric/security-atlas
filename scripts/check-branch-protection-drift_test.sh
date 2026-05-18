#!/usr/bin/env bash
#
# Smoke tests for scripts/check-branch-protection-drift.sh.
#
# AC-8 of slice 127: integration test exercising the script against
# fixture configs. Two fixtures: in-sync (file == live contexts list)
# and drift (file lists a context that live does not). Asserts the
# script exits with the expected code for each fixture.
#
# Test harness uses the ATLAS_FIXTURE_FILE + ATLAS_FIXTURE_LIVE env
# overrides so the script never touches the network. That makes this
# harness runnable in offline CI environments + local sandboxes without
# `gh` auth.
#
# Run: bash scripts/check-branch-protection-drift_test.sh
# Exits non-zero on first failed assertion.

set -eu

SCRIPT="$(cd "$(dirname "$0")" && pwd)/check-branch-protection-drift.sh"
if [[ ! -x "$SCRIPT" ]]; then
  echo "check-branch-protection-drift_test: script not executable at $SCRIPT" >&2
  exit 2
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "check-branch-protection-drift_test: jq required on PATH" >&2
  exit 2
fi

pass=0
fail=0
fail_messages=()

assert_eq() {
  local actual="$1" expected="$2" label="$3"
  if [[ "$actual" == "$expected" ]]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    fail_messages+=("$label: got exit '$actual', want exit '$expected'")
  fi
}

assert_contains() {
  local haystack="$1" needle="$2" label="$3"
  if grep -qF -- "$needle" <<<"$haystack"; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    fail_messages+=("$label: expected output to contain '$needle'")
    fail_messages+=("  actual output:")
    while IFS= read -r line; do
      fail_messages+=("    $line")
    done <<<"$haystack"
  fi
}

# --------------------------------------------------------------------
# Set up fixture tree
# --------------------------------------------------------------------
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT

# In-sync fixture: file + live carry the SAME context list.
cat >"$fixture/file_in_sync.json" <<'JSON'
{
  "$comment": "Test fixture — in-sync.",
  "required_status_checks": {
    "strict": true,
    "contexts": [
      "Go · build + test",
      "Go · lint",
      "pre-commit · all hooks"
    ]
  },
  "enforce_admins": true
}
JSON
cat >"$fixture/live_in_sync.json" <<'JSON'
["Go · build + test", "Go · lint", "pre-commit · all hooks"]
JSON

# Drift fixture: file lists 3 contexts, live lists only 2 (missing
# `Go · lint`) — same shape as the real 2026-05-18 drift incident
# scaled down.
cat >"$fixture/file_drift.json" <<'JSON'
{
  "$comment": "Test fixture — drift.",
  "required_status_checks": {
    "strict": true,
    "contexts": [
      "Go · build + test",
      "Go · lint",
      "pre-commit · all hooks"
    ]
  },
  "enforce_admins": true
}
JSON
cat >"$fixture/live_drift.json" <<'JSON'
["Go · build + test", "pre-commit · all hooks"]
JSON

# Malformed JSON fixture: should exit 2 (env misconfigured), not 1
# (drift). This guards the "valid JSON before compare" invariant.
cat >"$fixture/file_bad.json" <<'JSON'
{ this is not valid json
JSON

# Missing required_status_checks.contexts: should also exit 2 with a
# clear error.
cat >"$fixture/file_no_contexts.json" <<'JSON'
{
  "$comment": "No required_status_checks at all.",
  "enforce_admins": true
}
JSON

# --------------------------------------------------------------------
# Run cases
# --------------------------------------------------------------------

# Case 1 — in-sync — exit 0
set +e
output_1="$(ATLAS_FIXTURE_FILE="$fixture/file_in_sync.json" ATLAS_FIXTURE_LIVE="$fixture/live_in_sync.json" "$SCRIPT" 2>&1)"
rc_1=$?
set -e
assert_eq "$rc_1" "0" "case 1: in-sync should exit 0"
assert_contains "$output_1" "no drift detected" "case 1: in-sync output should say no drift detected"

# Case 2 — drift — exit 1
set +e
output_2="$(ATLAS_FIXTURE_FILE="$fixture/file_drift.json" ATLAS_FIXTURE_LIVE="$fixture/live_drift.json" "$SCRIPT" 2>&1)"
rc_2=$?
set -e
assert_eq "$rc_2" "1" "case 2: drift should exit 1"
assert_contains "$output_2" "drift detected" "case 2: drift output should say drift detected"
assert_contains "$output_2" "Go · lint" "case 2: drift output should name the missing context"
assert_contains "$output_2" "apply-branch-protection.sh" "case 2: drift output should hint at the apply script"

# Case 3 — malformed JSON — exit 2
set +e
output_3="$(ATLAS_FIXTURE_FILE="$fixture/file_bad.json" ATLAS_FIXTURE_LIVE="$fixture/live_in_sync.json" "$SCRIPT" 2>&1)"
rc_3=$?
set -e
assert_eq "$rc_3" "2" "case 3: malformed JSON should exit 2"
assert_contains "$output_3" "not valid JSON" "case 3: malformed JSON output should name the failure mode"

# Case 4 — missing required_status_checks.contexts — exit 2
set +e
output_4="$(ATLAS_FIXTURE_FILE="$fixture/file_no_contexts.json" ATLAS_FIXTURE_LIVE="$fixture/live_in_sync.json" "$SCRIPT" 2>&1)"
rc_4=$?
set -e
assert_eq "$rc_4" "2" "case 4: missing required_status_checks.contexts should exit 2"
assert_contains "$output_4" "no .required_status_checks.contexts list" "case 4: missing-list output should name the failure mode"

# Case 5 — ATLAS_FIXTURE_LIVE pointing at a missing file — exit 2
set +e
output_5="$(ATLAS_FIXTURE_FILE="$fixture/file_in_sync.json" ATLAS_FIXTURE_LIVE="$fixture/does-not-exist.json" "$SCRIPT" 2>&1)"
rc_5=$?
set -e
assert_eq "$rc_5" "2" "case 5: missing fixture live file should exit 2"
assert_contains "$output_5" "ATLAS_FIXTURE_LIVE set but file missing" "case 5: missing-live-file output should name the failure mode"

# Case 6 — re-run case 1 — confirms reproducibility (same fixture, same
# exit code on two consecutive invocations).
set +e
ATLAS_FIXTURE_FILE="$fixture/file_in_sync.json" ATLAS_FIXTURE_LIVE="$fixture/live_in_sync.json" "$SCRIPT" >/dev/null 2>&1
rc_6=$?
set -e
assert_eq "$rc_6" "0" "case 6: reproducibility — second run of case 1 should also exit 0"

# --------------------------------------------------------------------
# Report
# --------------------------------------------------------------------
echo ""
echo "check-branch-protection-drift_test: $pass passed, $fail failed"
if (( fail > 0 )); then
  for msg in "${fail_messages[@]}"; do
    echo "  $msg"
  done
  exit 1
fi
