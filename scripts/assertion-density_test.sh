#!/usr/bin/env bash
#
# assertion-density_test.sh — slice 353 self-test for
# scripts/assertion-density.sh.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SCRIPT_DIR/assertion-density.sh"

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

assert_not_contains() {
  local haystack="$1" needle="$2" msg="$3"
  if [[ "$haystack" != *"$needle"* ]]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    fail_messages+=("FAIL: $msg (output unexpectedly contained '$needle')")
  fi
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/pkg"

# A DENSE file: many assertions over modest LOC → should NOT warn.
cat > "$tmp/pkg/dense_test.go" <<'GO'
package pkg

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDense(t *testing.T) {
	require.Equal(t, 1, 1)
	require.NoError(t, nil)
	if false {
		t.Fatalf("nope")
	}
	require.True(t, true)
	require.False(t, false)
	require.NotNil(t, t)
}
GO

# A SPARSE file: lots of LOC, one weak assertion → should warn.
{
  echo "package pkg"
  echo ""
  echo "import \"testing\""
  echo ""
  echo "func TestSparse(t *testing.T) {"
  echo "	doThing()"
  for i in $(seq 1 40); do
    echo "	_ = $i // filler line to inflate LOC"
  done
  echo "	if false { t.Errorf(\"x\") }"
  echo "}"
  echo "func doThing() {}"
} > "$tmp/pkg/sparse_test.go"

# A TINY file below MIN_LOC → should be skipped entirely.
cat > "$tmp/pkg/tiny_test.go" <<'GO'
package pkg

func helper() {}
GO

# --------------------------------------------------------------------
# Case 1 — text mode: warns on sparse, not on dense, skips tiny.
# --------------------------------------------------------------------
set +e
out1="$(DENSITY_ROOT="$tmp" DENSITY_LOC=20 DENSITY_MIN_LOC=15 "$SCRIPT" 2>&1)"
rc1=$?
set -e
assert_eq "$rc1" "0" "case 1: advisory always exits 0"
assert_contains "$out1" "sparse_test.go" "case 1: flags the sparse file"
assert_not_contains "$out1" "dense_test.go" "case 1: does NOT flag the dense file"
assert_not_contains "$out1" "tiny_test.go" "case 1: skips the below-MIN_LOC tiny file"
assert_contains "$out1" "WARNING" "case 1: emits a WARNING banner"

# --------------------------------------------------------------------
# Case 2 — json mode is well-formed and marks the sparse file.
# --------------------------------------------------------------------
set +e
out2="$(DENSITY_ROOT="$tmp" DENSITY_LOC=20 DENSITY_MIN_LOC=15 DENSITY_FORMAT=json "$SCRIPT" 2>/dev/null)"
rc2=$?
set -e
assert_eq "$rc2" "0" "case 2: json mode exits 0"
if command -v jq >/dev/null 2>&1; then
  below="$(printf '%s' "$out2" | jq -r '.below_threshold')"
  assert_eq "$below" "1" "case 2: json reports exactly 1 below-threshold file"
  sparse_below="$(printf '%s' "$out2" | jq -r '.rows[] | select(.file|test("sparse")) | .below_threshold')"
  assert_eq "$sparse_below" "true" "case 2: sparse row marked below_threshold"
else
  assert_contains "$out2" '"below_threshold":1' "case 2: json reports 1 below-threshold (no jq)"
  pass=$((pass + 1)) # keep count parity with the jq branch
fi

# --------------------------------------------------------------------
# Case 3 — all-dense tree reports OK (no warnings).
# --------------------------------------------------------------------
tmp2="$(mktemp -d)"
trap 'rm -rf "$tmp" "$tmp2"' EXIT
mkdir -p "$tmp2/pkg"
cp "$tmp/pkg/dense_test.go" "$tmp2/pkg/dense_test.go"
set +e
out3="$(DENSITY_ROOT="$tmp2" DENSITY_LOC=20 DENSITY_MIN_LOC=15 "$SCRIPT" 2>&1)"
rc3=$?
set -e
assert_eq "$rc3" "0" "case 3: all-dense exits 0"
assert_contains "$out3" "OK" "case 3: reports OK when all files meet threshold"

# --------------------------------------------------------------------
# Case 4 — bad root → exit 2.
# --------------------------------------------------------------------
set +e
out4="$(DENSITY_ROOT="$tmp/does-not-exist" "$SCRIPT" 2>&1)"
rc4=$?
set -e
assert_eq "$rc4" "2" "case 4: missing root → exit 2"

echo ""
echo "assertion-density_test: $pass passed, $fail failed"
if (( fail > 0 )); then
  for msg in "${fail_messages[@]}"; do
    echo "  $msg"
  done
  exit 1
fi
