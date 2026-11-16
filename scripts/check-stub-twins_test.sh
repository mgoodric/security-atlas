#!/usr/bin/env bash
#
# check-stub-twins_test.sh — #2099 self-test for scripts/check-stub-twins.sh.
#
# Synthetic workflows plus a real-tree assertion.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SCRIPT_DIR/check-stub-twins.sh"
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

# run <workflow> [no-twin list] sets out and rc.
run() {
  set +e
  out="$(CI_WORKFLOW="$1" STUB_TWINS_NO_TWIN="${2:-}" bash "$SCRIPT" 2>&1)"
  rc=$?
  set -e
}

# write <file> <real-if> <stub-name> <stub-if>
write() {
  cat > "$1" <<EOF
name: CI
on: [pull_request]
jobs:
  changes:
    runs-on: ubuntu-latest
  build:
    name: Go · build
    needs: changes
    if: $2
    runs-on: ubuntu-latest
  build-stub:
    name: $3
    needs: changes
    if: $4
    runs-on: ubuntu-latest
EOF
}

# Case 1 — a matched pair on each output passes.
for x in code go web; do
  write "$tmp/ok-$x.yml" "needs.changes.outputs.$x == 'true'" "Go · build" "needs.changes.outputs.$x != 'true'"
  run "$tmp/ok-$x.yml"
  assert_eq "$rc" "0" "matched $x pair passes: $out"
done

# Case 2 — the pull_request-only shape and the always() shape pass.
write "$tmp/ok-pr.yml" "github.event_name == 'pull_request' && needs.changes.outputs.web == 'true'" \
  "Go · build" "github.event_name != 'pull_request' || needs.changes.outputs.web != 'true'"
run "$tmp/ok-pr.yml"
assert_eq "$rc" "0" "pull_request-shape pair passes: $out"
write "$tmp/ok-always.yml" "always() && needs.changes.outputs.go == 'true'" "Go · build" "\${{ needs.changes.outputs.go != 'true' }}"
run "$tmp/ok-always.yml"
assert_eq "$rc" "0" "always() real with \${{ }} stub passes: $out"

# Case 3 — a stub whose name drifted: the real job has no twin, the stub is an orphan.
write "$tmp/name.yml" "needs.changes.outputs.go == 'true'" "Go · build (stub)" "needs.changes.outputs.go != 'true'"
run "$tmp/name.yml"
assert_eq "$rc" "1" "renamed stub fails"
assert_contains "$out" "MISSING-TWIN build" "renamed stub: real job reported"
assert_contains "$out" "ORPHAN-STUB build-stub" "renamed stub: stub reported"

# Case 4 — the twin still reads `code` after the real job moved to `go`.
write "$tmp/tier.yml" "needs.changes.outputs.go == 'true'" "Go · build" "needs.changes.outputs.code != 'true'"
run "$tmp/tier.yml"
assert_eq "$rc" "1" "twin on the wrong output fails"
assert_contains "$out" "NOT-COMPLEMENT build / build-stub" "wrong output reported"

# Case 5 — the twin is the same gate, not the complement.
write "$tmp/same.yml" "needs.changes.outputs.web == 'true'" "Go · build" "needs.changes.outputs.web == 'true'"
run "$tmp/same.yml"
assert_eq "$rc" "1" "two real jobs under one name fails"
assert_contains "$out" "NOT-COMPLEMENT" "same-gate twin reported"

# Case 6 — mixing the pull_request shape with the plain shape is not a complement.
write "$tmp/mix.yml" "github.event_name == 'pull_request' && needs.changes.outputs.web == 'true'" \
  "Go · build" "needs.changes.outputs.web != 'true'"
run "$tmp/mix.yml"
assert_eq "$rc" "1" "pull_request real with plain stub fails"

# Case 7 — an unrecognised gate shape on a tier output.
write "$tmp/odd.yml" "needs.changes.outputs.go == 'true' || github.ref == 'refs/heads/main'" \
  "Go · build" "needs.changes.outputs.go != 'true'"
run "$tmp/odd.yml"
assert_eq "$rc" "1" "unrecognised gate fails"
assert_contains "$out" "UNRECOGNISED build" "unrecognised gate reported"

# Case 8 — a NO_TWIN entry waives a real job with no stub; a stale one fails.
cat > "$tmp/notwin.yml" <<'EOF'
jobs:
  changes:
    runs-on: ubuntu-latest
  shared:
    name: Go · shared
    if: needs.changes.outputs.code == 'true'
EOF
run "$tmp/notwin.yml"
assert_eq "$rc" "1" "real job without a twin fails"
run "$tmp/notwin.yml" "shared not required"
assert_eq "$rc" "0" "NO_TWIN waives it: $out"
run "$tmp/notwin.yml" "shared not required
gone no longer exists"
assert_eq "$rc" "1" "stale NO_TWIN entry fails"
assert_contains "$out" "stale NO_TWIN entry 'gone'" "stale entry reported"
run "$tmp/notwin.yml" "shared"
assert_eq "$rc" "1" "NO_TWIN entry without a reason fails"

# Case 9 — three jobs under one gated name.
cat > "$tmp/dup.yml" <<'EOF'
jobs:
  a:
    name: X
    if: needs.changes.outputs.go == 'true'
  b:
    name: X
    if: needs.changes.outputs.go != 'true'
  c:
    name: X
    if: needs.changes.outputs.go != 'true'
EOF
run "$tmp/dup.yml"
assert_eq "$rc" "1" "three jobs under one name fails"
assert_contains "$out" "DUPLICATE 'X'" "duplicate reported"

# Case 10 — unreadable inputs exit 2.
run "$tmp/does-not-exist.yml"
assert_eq "$rc" "2" "missing workflow exits 2"
printf 'jobs: [unclosed\n' > "$tmp/bad.yml"
run "$tmp/bad.yml"
assert_eq "$rc" "2" "invalid YAML exits 2"

# Case 11 — the real ci.yml passes with the real NO_TWIN list.
set +e
out="$(cd "$REPO_ROOT" && bash "$SCRIPT" 2>&1)"
rc=$?
set -e
assert_eq "$rc" "0" "real ci.yml passes: $out"

for m in "${fail_messages[@]+"${fail_messages[@]}"}"; do
  echo "$m" >&2
done
echo "check-stub-twins_test: $pass passed, $fail failed"
[[ $fail -eq 0 ]]
