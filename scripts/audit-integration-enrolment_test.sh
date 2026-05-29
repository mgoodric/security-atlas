#!/usr/bin/env bash
#
# Smoke tests for scripts/audit-integration-enrolment.sh (slice 345).
#
# AC-2 + AC-3 + AC-5: synthetic positive + negative fixtures exercise the
# guard's pass/fail paths against a fabricated tree + ci.yml, without
# touching the real repo. Same harness shape as
# scripts/check-branch-protection-drift_test.sh (slice 127) and the
# slice 382 branch-name guard.
#
# The script accepts two test-only env overrides:
#   AUDIT_ENROL_GREP_ROOT — directory the guard greps for the build tag
#   AUDIT_ENROL_CI_YML     — the ci.yml whose package list is parsed
#
# Note on the allowlist: the guard ships a hardcoded KNOWN_UNENROLLED
# allowlist sized for the REAL tree. These fixtures use package names
# that do NOT appear on that allowlist (e.g. internal/fixturepkg_*), so
# the allowlist never masks a fixture's expected outcome.
#
# Run: bash scripts/audit-integration-enrolment_test.sh
# Exits non-zero on first failed assertion.

set -eu

SCRIPT="$(cd "$(dirname "$0")" && pwd)/audit-integration-enrolment.sh"
if [[ ! -x "$SCRIPT" ]]; then
  echo "audit-integration-enrolment_test: script not executable at $SCRIPT" >&2
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
# Fixture tree. Two tagged packages live under <root>/internal:
#   internal/fixturepkg_enrolled   — tagged AND listed in fixture ci.yml
#   internal/fixturepkg_forgotten  — tagged but NOT listed (the gap)
# A third, untagged package confirms untagged dirs are ignored.
# --------------------------------------------------------------------
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT

mkdir -p "$fixture/internal/fixturepkg_enrolled"
mkdir -p "$fixture/internal/fixturepkg_forgotten"
mkdir -p "$fixture/internal/fixturepkg_untagged"

cat >"$fixture/internal/fixturepkg_enrolled/x_integration_test.go" <<'GO'
//go:build integration

package fixturepkg_enrolled
GO

cat >"$fixture/internal/fixturepkg_forgotten/x_integration_test.go" <<'GO'
//go:build integration

package fixturepkg_forgotten
GO

cat >"$fixture/internal/fixturepkg_untagged/x_test.go" <<'GO'
package fixturepkg_untagged
GO

# A ci.yml that enrols ONLY the enrolled fixture package. The forgotten
# one is deliberately absent — that is what the negative case detects.
ci_pass="$fixture/ci_pass.yml"
cat >"$ci_pass" <<'YML'
jobs:
  tests-integration:
    steps:
      - run: |
          go test -tags=integration -p 1 \
            ./internal/fixturepkg_enrolled/... \
            ./internal/fixturepkg_forgotten/...
YML

# A ci.yml that enrols ONLY the enrolled fixture package, leaving the
# forgotten one unlisted (the failing case).
ci_fail="$fixture/ci_fail.yml"
cat >"$ci_fail" <<'YML'
jobs:
  tests-integration:
    steps:
      - run: |
          go test -tags=integration -p 1 \
            ./internal/fixturepkg_enrolled/...
YML

# --------------------------------------------------------------------
# Case 1 — POSITIVE: every tagged fixture package is listed → exit 0.
# --------------------------------------------------------------------
set +e
out1="$(AUDIT_ENROL_GREP_ROOT="$fixture/internal" AUDIT_ENROL_CI_YML="$ci_pass" "$SCRIPT" 2>&1)"
rc1=$?
set -e
assert_eq "$rc1" "0" "case 1 (positive): all tagged packages listed → exit 0"
assert_contains "$out1" "OK" "case 1: positive run reports OK"

# --------------------------------------------------------------------
# Case 2 — NEGATIVE: a tagged fixture package is NOT listed → exit 1,
# and the offending package is named in the failure message (AC-2).
# --------------------------------------------------------------------
set +e
out2="$(AUDIT_ENROL_GREP_ROOT="$fixture/internal" AUDIT_ENROL_CI_YML="$ci_fail" "$SCRIPT" 2>&1)"
rc2=$?
set -e
assert_eq "$rc2" "1" "case 2 (negative): unlisted tagged package → exit 1"
assert_contains "$out2" "FAIL" "case 2: negative run reports FAIL"
assert_contains "$out2" "fixturepkg_forgotten" "case 2: failure names the forgotten package"
# The enrolled package should NOT appear in the missing list. Assert it
# is absent from the bullet lines (lines beginning with '    - ').
if grep -E '^\s+- \./internal/fixturepkg_enrolled/' <<<"$out2" >/dev/null; then
  fail=$((fail + 1))
  fail_messages+=("case 2: enrolled package wrongly flagged as missing")
else
  pass=$((pass + 1))
fi

# --------------------------------------------------------------------
# Case 3 — MISCONFIG: ci.yml with no ./internal/... entries → exit 2.
# --------------------------------------------------------------------
ci_empty="$fixture/ci_empty.yml"
cat >"$ci_empty" <<'YML'
jobs:
  tests-integration:
    steps:
      - run: echo "no package list here"
YML
set +e
out3="$(AUDIT_ENROL_GREP_ROOT="$fixture/internal" AUDIT_ENROL_CI_YML="$ci_empty" "$SCRIPT" 2>&1)"
rc3=$?
set -e
assert_eq "$rc3" "2" "case 3 (misconfig): empty package list → exit 2"
assert_contains "$out3" "found no ./internal/... package entries" "case 3: names the failure mode"

# --------------------------------------------------------------------
# Case 4 — MISCONFIG: unreadable ci.yml → exit 2.
# --------------------------------------------------------------------
set +e
out4="$(AUDIT_ENROL_GREP_ROOT="$fixture/internal" AUDIT_ENROL_CI_YML="$fixture/does-not-exist.yml" "$SCRIPT" 2>&1)"
rc4=$?
set -e
assert_eq "$rc4" "2" "case 4 (misconfig): missing ci.yml → exit 2"
assert_contains "$out4" "not readable" "case 4: names the failure mode"

# --------------------------------------------------------------------
# Case 5 — REAL TREE: the guard passes against the actual repo (the
# allowlist absorbs the current backlog). This is the AC-3 / "passes
# now" assertion executed against live state, not a fixture.
# --------------------------------------------------------------------
set +e
out5="$("$SCRIPT" 2>&1)"
rc5=$?
set -e
assert_eq "$rc5" "0" "case 5 (real tree): guard passes against the live repo"
assert_contains "$out5" "OK" "case 5: real-tree run reports OK"

# --------------------------------------------------------------------
# Report
# --------------------------------------------------------------------
echo ""
echo "audit-integration-enrolment_test: $pass passed, $fail failed"
if (( fail > 0 )); then
  for msg in "${fail_messages[@]}"; do
    echo "  $msg"
  done
  exit 1
fi
