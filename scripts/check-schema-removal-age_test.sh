#!/usr/bin/env bash
#
# Smoke tests for scripts/check-schema-removal-age.sh.
#
# AC-10 of slice 179: fixture-driven test cases for the four
# documented scenarios:
#   (a) all removals satisfy floor      -> exit 0
#   (b) one removal violates floor      -> exit 1
#   (c) override env var bypasses (b)   -> exit 0
#   (d) no removals at all              -> exit 0 (quiet)
#
# Test harness builds a real ephemeral git repository so the script's
# `git log` trust-root reads real git output (NOT mocked) — matches
# D5 in docs/audit-log/179-schema-removal-age-decisions.md. Fake
# commit dates via GIT_AUTHOR_DATE + GIT_COMMITTER_DATE; "now" is
# pinned via SCHEMA_REMOVAL_NOW so age arithmetic is deterministic.
#
# Run: bash scripts/check-schema-removal-age_test.sh
# Exits non-zero on first failed assertion.

set -eu

SCRIPT="$(cd "$(dirname "$0")" && pwd)/check-schema-removal-age.sh"
if [[ ! -x "$SCRIPT" ]]; then
  echo "check-schema-removal-age_test: script not executable at $SCRIPT" >&2
  exit 2
fi

if ! command -v git >/dev/null 2>&1; then
  echo "check-schema-removal-age_test: git required on PATH" >&2
  exit 2
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "check-schema-removal-age_test: python3 required on PATH" >&2
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

assert_not_contains() {
  local haystack="$1" needle="$2" label="$3"
  if grep -qF -- "$needle" <<<"$haystack"; then
    fail=$((fail + 1))
    fail_messages+=("$label: did NOT expect output to contain '$needle'")
    fail_messages+=("  actual output:")
    while IFS= read -r line; do
      fail_messages+=("    $line")
    done <<<"$haystack"
  else
    pass=$((pass + 1))
  fi
}

# --------------------------------------------------------------------
# Build a real ephemeral git repository with three schema files,
# each introduced at a different fake commit date.
# --------------------------------------------------------------------
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT

cd "$fixture"
git init -q -b main .
git config user.email "test@example.invalid"
git config user.name "Slice 179 Test"
git config commit.gpgsign false

mkdir -p schemas/old.kind schemas/young.kind schemas/medium.kind

# Pin "now" at 2026-05-20T00:00:00Z.
NOW="2026-05-20T00:00:00Z"

# OLD: introduced 200 days before NOW -> well above the 90-day floor.
OLD_DATE="2025-11-01T00:00:00Z"
# MEDIUM: introduced 95 days before NOW -> just above floor.
MED_DATE="2026-02-14T00:00:00Z"
# YOUNG: introduced 30 days before NOW -> well below floor.
YOUNG_DATE="2026-04-20T00:00:00Z"

commit_at() {
  local when="$1" msg="$2"
  GIT_AUTHOR_DATE="$when" GIT_COMMITTER_DATE="$when" \
    git commit -q -m "$msg"
}

# OLD commit.
echo '{"x-semver":"1.0.0"}' > schemas/old.kind/1.0.0.json
git add schemas/old.kind/1.0.0.json
commit_at "$OLD_DATE" "add old.kind/1.0.0.json"

# MEDIUM commit.
echo '{"x-semver":"1.0.0"}' > schemas/medium.kind/1.0.0.json
git add schemas/medium.kind/1.0.0.json
commit_at "$MED_DATE" "add medium.kind/1.0.0.json"

# YOUNG commit.
echo '{"x-semver":"1.0.0"}' > schemas/young.kind/1.0.0.json
git add schemas/young.kind/1.0.0.json
commit_at "$YOUNG_DATE" "add young.kind/1.0.0.json"

# Sanity: list commits + dates so test output is readable when debugging.
echo "fixture commits:"
git log --format='  %cI  %s' main

# --------------------------------------------------------------------
# Case (d) — no removals at all -> quiet exit 0.
# --------------------------------------------------------------------
set +e
out_d="$(SCHEMA_REMOVAL_NOW="$NOW" "$SCRIPT" < /dev/null 2>&1)"
rc_d=$?
set -e
assert_eq "$rc_d" "0" "case(d) no input -> exit 0"
assert_contains "$out_d" "no removed schema files supplied" "case(d) quiet message"

# --------------------------------------------------------------------
# Case (a) — all removals satisfy floor (OLD + MEDIUM, both >= 90 days
# above NOW). Expect exit 0.
# --------------------------------------------------------------------
set +e
out_a="$(SCHEMA_REMOVAL_NOW="$NOW" "$SCRIPT" \
  schemas/old.kind/1.0.0.json schemas/medium.kind/1.0.0.json 2>&1)"
rc_a=$?
set -e
assert_eq "$rc_a" "0" "case(a) all-pass -> exit 0"
assert_contains "$out_a" "ok: schemas/old.kind/1.0.0.json" "case(a) old ok line"
assert_contains "$out_a" "ok: schemas/medium.kind/1.0.0.json" "case(a) medium ok line"
assert_contains "$out_a" "all 2 removal(s) satisfy" "case(a) summary line"
assert_not_contains "$out_a" "violate" "case(a) no violation line"

# --------------------------------------------------------------------
# Case (b) — one removal (YOUNG, 30 days) violates floor. Expect
# exit 1 with the AC-3 error message naming the file + age + remaining.
# --------------------------------------------------------------------
set +e
out_b="$(SCHEMA_REMOVAL_NOW="$NOW" "$SCRIPT" \
  schemas/young.kind/1.0.0.json schemas/old.kind/1.0.0.json 2>&1)"
rc_b=$?
set -e
assert_eq "$rc_b" "1" "case(b) one-violation -> exit 1"
assert_contains "$out_b" "schemas/young.kind/1.0.0.json introduced" "case(b) violation names file"
assert_contains "$out_b" "age 30 days" "case(b) violation reports computed age"
assert_contains "$out_b" "remaining: 60 days" "case(b) violation reports remaining days"
assert_contains "$out_b" "Override with [deprecation-override]" "case(b) violation hints override path"
# OLD should still appear as ok.
assert_contains "$out_b" "ok: schemas/old.kind/1.0.0.json" "case(b) old still ok in same run"

# --------------------------------------------------------------------
# Case (c) — same as (b) but with override env var. Expect exit 0
# and the override-acknowledgement message on stderr.
# --------------------------------------------------------------------
set +e
out_c="$(SCHEMA_REMOVAL_NOW="$NOW" SCHEMA_REMOVAL_OVERRIDE=1 "$SCRIPT" \
  schemas/young.kind/1.0.0.json schemas/old.kind/1.0.0.json 2>&1)"
rc_c=$?
set -e
assert_eq "$rc_c" "0" "case(c) override -> exit 0"
assert_contains "$out_c" "schemas/young.kind/1.0.0.json introduced" "case(c) violation still reported"
assert_contains "$out_c" "SCHEMA_REMOVAL_OVERRIDE=1 set — bypassing failure" "case(c) override acknowledgement"

# --------------------------------------------------------------------
# Edge case (e) — path not in main history. Expect exit 2 (misuse)
# and a clear stderr message. P0-179-6.
# --------------------------------------------------------------------
set +e
out_e="$(SCHEMA_REMOVAL_NOW="$NOW" "$SCRIPT" \
  schemas/ghost.kind/1.0.0.json 2>&1)"
rc_e=$?
set -e
assert_eq "$rc_e" "2" "case(e) unknown-path -> exit 2 misuse"
assert_contains "$out_e" "no introduction commit" "case(e) clear message"
assert_contains "$out_e" "schemas/ghost.kind/1.0.0.json" "case(e) names the offending path"

# --------------------------------------------------------------------
# Edge case (f) — stdin form. Pipe the path list. Expect same behavior
# as positional args.
# --------------------------------------------------------------------
set +e
out_f="$(printf 'schemas/young.kind/1.0.0.json\n' | \
  SCHEMA_REMOVAL_NOW="$NOW" "$SCRIPT" 2>&1)"
rc_f=$?
set -e
assert_eq "$rc_f" "1" "case(f) stdin form -> same violation behavior"
assert_contains "$out_f" "schemas/young.kind/1.0.0.json introduced" "case(f) stdin path picked up"

# --------------------------------------------------------------------
# Edge case (g) — boundary: a file introduced exactly 90 days before
# NOW MUST pass (>= floor). Create a synthetic commit at NOW-90d and
# verify the script accepts it.
# --------------------------------------------------------------------
BOUNDARY_DATE="2026-02-19T00:00:00Z"  # 90 full days before 2026-05-20Z
mkdir -p schemas/boundary.kind
echo '{"x-semver":"1.0.0"}' > schemas/boundary.kind/1.0.0.json
git add schemas/boundary.kind/1.0.0.json
GIT_AUTHOR_DATE="$BOUNDARY_DATE" GIT_COMMITTER_DATE="$BOUNDARY_DATE" \
  git commit -q -m "add boundary.kind/1.0.0.json at exactly NOW-90d"

set +e
out_g="$(SCHEMA_REMOVAL_NOW="$NOW" "$SCRIPT" \
  schemas/boundary.kind/1.0.0.json 2>&1)"
rc_g=$?
set -e
assert_eq "$rc_g" "0" "case(g) exact 90-day boundary -> exit 0 (>=)"
assert_contains "$out_g" "age 90 days" "case(g) reports exactly 90 days"

# --------------------------------------------------------------------
# Edge case (h) — multiple violations report ALL of them, not just
# the first. Create a second young file and assert both surface.
# --------------------------------------------------------------------
YOUNG2_DATE="2026-04-25T00:00:00Z"
mkdir -p schemas/young2.kind
echo '{"x-semver":"1.0.0"}' > schemas/young2.kind/1.0.0.json
git add schemas/young2.kind/1.0.0.json
GIT_AUTHOR_DATE="$YOUNG2_DATE" GIT_COMMITTER_DATE="$YOUNG2_DATE" \
  git commit -q -m "add young2.kind/1.0.0.json"

set +e
out_h="$(SCHEMA_REMOVAL_NOW="$NOW" "$SCRIPT" \
  schemas/young.kind/1.0.0.json schemas/young2.kind/1.0.0.json 2>&1)"
rc_h=$?
set -e
assert_eq "$rc_h" "1" "case(h) two violations -> exit 1"
assert_contains "$out_h" "schemas/young.kind/1.0.0.json introduced" "case(h) first violation listed"
assert_contains "$out_h" "schemas/young2.kind/1.0.0.json introduced" "case(h) second violation listed"
assert_contains "$out_h" "2 removal(s) violate" "case(h) violation summary line"

# --------------------------------------------------------------------
# Report
# --------------------------------------------------------------------
echo ""
echo "check-schema-removal-age_test: $pass passed, $fail failed"
if [[ $fail -gt 0 ]]; then
  echo ""
  for msg in "${fail_messages[@]}"; do
    echo "  $msg"
  done
  exit 1
fi
exit 0
