#!/usr/bin/env bash
#
# Smoke tests for scripts/check-action-pins.sh.
#
# Slice 128: integration test exercising the script against fixture
# workflow trees. Six cases: pinned (passes), tag-pinned (fails),
# mixed (fails on the one tag-pinned line), short SHA (fails — must
# be a full 40-char), workflows-dir missing (env error), empty dir
# (env error).
#
# Test harness uses the ATLAS_WORKFLOWS_DIR env override so the script
# never touches the real in-tree .github/workflows/. That makes this
# harness runnable in offline sandboxes without needing to mutate or
# restore the live tree.
#
# Run: bash scripts/check-action-pins_test.sh
# Exits non-zero on first failed assertion.

set -eu

SCRIPT="$(cd "$(dirname "$0")" && pwd)/check-action-pins.sh"
if [[ ! -x "$SCRIPT" ]]; then
  echo "check-action-pins_test: script not executable at $SCRIPT" >&2
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
# Set up fixture trees
# --------------------------------------------------------------------
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT

# Fixture 1 — every uses: line is SHA-pinned.
mkdir -p "$fixture/pinned"
cat >"$fixture/pinned/ok.yml" <<'YAML'
name: ok
on: [push]
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6
      - uses: actions/setup-go@4a3601121dd01d1626a1e23e37211e3254c1c06c # v6
      - uses: github/codeql-action/init@9e0d7b8d25671d64c341c19c0152d693099fb5ba # v4
YAML

# Fixture 2 — every uses: line is tag-pinned.
mkdir -p "$fixture/tagged"
cat >"$fixture/tagged/bad.yml" <<'YAML'
name: bad
on: [push]
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
      - uses: actions/setup-go@v6
YAML

# Fixture 3 — mixed (one SHA-pinned, one tag-pinned). The script
# should fail and name the tag-pinned line specifically.
mkdir -p "$fixture/mixed"
cat >"$fixture/mixed/mixed.yml" <<'YAML'
name: mixed
on: [push]
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6
      - uses: actions/setup-node@v6
YAML

# Fixture 4 — short SHA (only 12 chars). Must fail; full 40-char
# SHA is the discipline anti-criterion P0-A2 specifies.
mkdir -p "$fixture/shortsha"
cat >"$fixture/shortsha/short.yml" <<'YAML'
name: short
on: [push]
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@de0fac2e4500 # v6
YAML

# Fixture 5 — workflows dir does not exist at all.
# (We point at a path that does not exist.)
missing_dir="$fixture/does-not-exist"

# Fixture 6 — workflows dir exists but is empty (no .yml files).
mkdir -p "$fixture/empty"

# --------------------------------------------------------------------
# Run cases
# --------------------------------------------------------------------

# Case 1 — pinned — exit 0
set +e
output_1="$(ATLAS_WORKFLOWS_DIR="$fixture/pinned" "$SCRIPT" 2>&1)"
rc_1=$?
set -e
assert_eq "$rc_1" "0" "case 1: SHA-pinned should exit 0"
assert_contains "$output_1" "no tag-pinned actions detected" "case 1: SHA-pinned output should report success"

# Case 2 — tag-pinned — exit 1 + diagnostic naming each offending line
set +e
output_2="$(ATLAS_WORKFLOWS_DIR="$fixture/tagged" "$SCRIPT" 2>&1)"
rc_2=$?
set -e
assert_eq "$rc_2" "1" "case 2: tag-pinned should exit 1"
assert_contains "$output_2" "actions/checkout@v6" "case 2: diagnostic should name the offending uses: text"
assert_contains "$output_2" "actions/setup-go@v6" "case 2: diagnostic should name BOTH offending lines"
assert_contains "$output_2" "tag-jacking" "case 2: rationale message should mention the threat class"

# Case 3 — mixed — exit 1 and name ONLY the tag-pinned line
set +e
output_3="$(ATLAS_WORKFLOWS_DIR="$fixture/mixed" "$SCRIPT" 2>&1)"
rc_3=$?
set -e
assert_eq "$rc_3" "1" "case 3: mixed should exit 1"
assert_contains "$output_3" "actions/setup-node@v6" "case 3: diagnostic should name the tag-pinned line"

# Case 4 — short SHA — exit 1
set +e
output_4="$(ATLAS_WORKFLOWS_DIR="$fixture/shortsha" "$SCRIPT" 2>&1)"
rc_4=$?
set -e
assert_eq "$rc_4" "1" "case 4: short SHA should exit 1"
assert_contains "$output_4" "is not a 40-char SHA" "case 4: diagnostic should name the length-failure mode"

# Case 5 — workflows dir missing — exit 2
set +e
output_5="$(ATLAS_WORKFLOWS_DIR="$missing_dir" "$SCRIPT" 2>&1)"
rc_5=$?
set -e
assert_eq "$rc_5" "2" "case 5: missing workflows dir should exit 2"
assert_contains "$output_5" "workflows directory does not exist" "case 5: missing-dir output should name the failure mode"

# Case 6 — empty workflows dir — exit 2
set +e
output_6="$(ATLAS_WORKFLOWS_DIR="$fixture/empty" "$SCRIPT" 2>&1)"
rc_6=$?
set -e
assert_eq "$rc_6" "2" "case 6: empty workflows dir should exit 2"
assert_contains "$output_6" "no .yml files found" "case 6: empty-dir output should name the failure mode"

# Case 7 — reproducibility: re-run case 1
set +e
ATLAS_WORKFLOWS_DIR="$fixture/pinned" "$SCRIPT" >/dev/null 2>&1
rc_7=$?
set -e
assert_eq "$rc_7" "0" "case 7: reproducibility — second run of case 1 should also exit 0"

# --------------------------------------------------------------------
# Report
# --------------------------------------------------------------------
echo ""
echo "check-action-pins_test: $pass passed, $fail failed"
if (( fail > 0 )); then
  for msg in "${fail_messages[@]}"; do
    echo "  $msg"
  done
  exit 1
fi
