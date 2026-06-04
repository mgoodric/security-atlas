#!/usr/bin/env bash
#
# check-integration-shard-coverage_test.sh — slice 417 self-test for
# scripts/check-integration-shard-coverage.sh.
#
# Drives the guard against synthetic manifest + tagged-package-tree
# fixtures via the SHARD_MANIFEST / SHARD_TAGGED_ROOT overrides so the
# pass/fail/disjoint/pin paths are deterministic and do not depend on the
# real tree.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SCRIPT_DIR/check-integration-shard-coverage.sh"

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
    fail_messages+=("FAIL: $msg (output missing '$needle')")
  fi
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Build a synthetic tagged-package tree with four integration-tagged
# packages: internal/db, internal/api/scfimport, internal/auth,
# internal/api/risks. Each gets one *_test.go carrying the build tag.
ROOT="$tmp/tree/internal"
make_tagged() {
  local pkgdir="$ROOT/$1"
  mkdir -p "$pkgdir"
  printf '//go:build integration\n\npackage x\n' > "$pkgdir/integration_test.go"
}
make_tagged db
make_tagged api/scfimport
make_tagged auth
make_tagged api/risks
TR="$tmp/tree/internal"

# --------------------------------------------------------------------
# Case 1 — complete, disjoint, pin-correct manifest → exit 0.
# --------------------------------------------------------------------
M1="$tmp/m1.txt"
cat > "$M1" <<'MAN'
# header comment
A   ./internal/db/...
A   ./internal/api/scfimport/...
B1  ./internal/auth
B2  ./internal/api/risks/...
MAN
set +e
out1="$(SHARD_MANIFEST="$M1" SHARD_TAGGED_ROOT="$TR" "$SCRIPT" 2>&1)"
rc1=$?
set -e
assert_eq "$rc1" "0" "case 1: complete+disjoint+pinned → exit 0"
assert_contains "$out1" "OK" "case 1: OK banner"

# --------------------------------------------------------------------
# Case 2 — T-1: a tagged package owned by NO leg → exit 1.
# --------------------------------------------------------------------
M2="$tmp/m2.txt"
cat > "$M2" <<'MAN'
A   ./internal/db/...
A   ./internal/api/scfimport/...
B1  ./internal/auth
MAN
set +e
out2="$(SHARD_MANIFEST="$M2" SHARD_TAGGED_ROOT="$TR" "$SCRIPT" 2>&1)"
rc2=$?
set -e
assert_eq "$rc2" "1" "case 2: tagged package owned by no leg → exit 1"
assert_contains "$out2" "T-1" "case 2: names the T-1 threat"
assert_contains "$out2" "internal/api/risks" "case 2: names the dropped package"

# --------------------------------------------------------------------
# Case 3 — double-assignment (a package on two legs) → exit 1.
# --------------------------------------------------------------------
M3="$tmp/m3.txt"
cat > "$M3" <<'MAN'
A   ./internal/db/...
A   ./internal/api/scfimport/...
B1  ./internal/auth
B2  ./internal/api/risks/...
B3  ./internal/auth
MAN
set +e
out3="$(SHARD_MANIFEST="$M3" SHARD_TAGGED_ROOT="$TR" "$SCRIPT" 2>&1)"
rc3=$?
set -e
assert_eq "$rc3" "1" "case 3: double-assignment → exit 1"
assert_contains "$out3" "MORE THAN ONE leg" "case 3: names the double-assignment"

# --------------------------------------------------------------------
# Case 4 — P0-2: a pinned catalog-seed package on a B-leg → exit 1.
# --------------------------------------------------------------------
M4="$tmp/m4.txt"
cat > "$M4" <<'MAN'
A   ./internal/db/...
B3  ./internal/api/scfimport/...
B1  ./internal/auth
B2  ./internal/api/risks/...
MAN
set +e
out4="$(SHARD_MANIFEST="$M4" SHARD_TAGGED_ROOT="$TR" "$SCRIPT" 2>&1)"
rc4=$?
set -e
assert_eq "$rc4" "1" "case 4: catalog seeder on B-leg → exit 1"
assert_contains "$out4" "P0-2" "case 4: names the P0-2 pin violation"

# --------------------------------------------------------------------
# Case 5 — stale manifest entry (in manifest, not tagged) → exit 1.
# --------------------------------------------------------------------
M5="$tmp/m5.txt"
cat > "$M5" <<'MAN'
A   ./internal/db/...
A   ./internal/api/scfimport/...
B1  ./internal/auth
B2  ./internal/api/risks/...
B3  ./internal/api/ghost/...
MAN
set +e
out5="$(SHARD_MANIFEST="$M5" SHARD_TAGGED_ROOT="$TR" "$SCRIPT" 2>&1)"
rc5=$?
set -e
assert_eq "$rc5" "1" "case 5: stale manifest entry → exit 1"
assert_contains "$out5" "internal/api/ghost" "case 5: names the stale package"

# --------------------------------------------------------------------
# Case 6 — unreadable manifest → exit 2.
# --------------------------------------------------------------------
set +e
SHARD_MANIFEST="$tmp/nope.txt" SHARD_TAGGED_ROOT="$TR" "$SCRIPT" >/dev/null 2>&1
rc6=$?
set -e
assert_eq "$rc6" "2" "case 6: missing manifest → exit 2"

# --------------------------------------------------------------------
# Case 7 — REAL tree: the shipped manifest + real internal/ tree pass.
# --------------------------------------------------------------------
set +e
out7="$("$SCRIPT" 2>&1)"
rc7=$?
set -e
assert_eq "$rc7" "0" "case 7: real manifest + real tree → exit 0"
assert_contains "$out7" "Phase-A catalog-seed pin holds" "case 7: real-tree pin holds"

echo ""
echo "check-integration-shard-coverage_test: $pass passed, $fail failed"
if (( fail > 0 )); then
  for msg in "${fail_messages[@]}"; do
    echo "  $msg"
  done
  exit 1
fi
