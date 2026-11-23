#!/usr/bin/env bash
#
# check-harness-engine_test.sh — #3016 self-test for
# scripts/check-harness-engine.sh. Synthetic trees plus a real-tree
# assertion.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SCRIPT_DIR/check-harness-engine.sh"

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
    fail_messages+=("FAIL: $msg (output contained '$needle')")
  fi
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# harness <path> <body...> writes an integration test file under the
# current fixture root whose body lines follow the build tag.
harness() {
  local path="$1"
  shift
  mkdir -p "$root/$(dirname "$path")"
  {
    printf '//go:build integration\n\npackage x_test\n\n'
    printf '%s\n' "$@"
  } >"$root/$path"
}

# run sets out and rc for the current fixture root and backlog.
run() {
  set +e
  out="$(HARNESS_ENGINE_ROOT="$root" HARNESS_ENGINE_BACKLOG="$root/backlog.txt" bash "$SCRIPT" 2>&1)"
  rc=$?
  set -e
}

fresh() {
  root="$tmp/$1"
  mkdir -p "$root/internal"
  : >"$root/backlog.txt"
}

# 1. An engine in the file passes, by each recognised call.
fresh engine
harness internal/a/a_test.go 'srv := api.New(api.Config{})' 'liveroletest.Attach(t, srv, app)'
harness internal/b/b_test.go 'srv := api.New(api.Config{})' 'srv.AttachAuthz(engine, nil)'
harness internal/c/c_test.go 'srv := api.New(api.Config{})' 'attachAuthz(t, srv, app)'
harness internal/d/d_test.go 'srv := api.New(api.Config{})' 'e, _ := authz.NewEngine(ctx, r)'
run
assert_eq "$rc" "0" "engine in file: exit 0"
assert_contains "$out" "check-harness-engine: ok" "engine in file: ok line"

# 2. No engine fails, naming the file.
fresh noengine
harness internal/a/a_test.go 'srv := api.New(api.Config{})'
run
assert_eq "$rc" "1" "no engine: exit 1"
assert_contains "$out" "NO-ENGINE   internal/a/a_test.go" "no engine: finding names the file"

# 3. Out of scope: a unit test, another package's New, a test with no api.New.
fresh scope
mkdir -p "$root/internal/u"
printf 'package u_test\n\nsrv := api.New(api.Config{})\n' >"$root/internal/u/u_test.go"
harness internal/v/v_test.go 'h := authapi.New(authn)' 'x := myapi.New()'
harness internal/w/w_test.go 'x := 1'
run
assert_eq "$rc" "0" "out of scope: exit 0"

# 4. An opt-out with a reason passes; one without a reason fails.
fresh optout
harness internal/a/a_test.go '// harness-engine: none (tests the engine-less router)' 'srv := api.New(api.Config{})'
run
assert_eq "$rc" "0" "opt-out with reason: exit 0"
harness internal/b/b_test.go '// harness-engine: none' 'srv := api.New(api.Config{})'
run
assert_eq "$rc" "1" "opt-out without reason: exit 1"
assert_contains "$out" "BAD-OPTOUT  internal/b/b_test.go" "opt-out without reason: finding"
assert_not_contains "$out" "internal/a/a_test.go" "opt-out with reason: not reported"

# 5. A backlog entry passes; a stale one fails three ways.
fresh backlog
harness internal/a/a_test.go 'srv := api.New(api.Config{})'
printf '# comment\n\ninternal/a/a_test.go\n' >"$root/backlog.txt"
run
assert_eq "$rc" "0" "backlog entry: exit 0"

harness internal/b/b_test.go 'srv := api.New(api.Config{})' 'liveroletest.Attach(t, srv, app)'
harness internal/c/c_test.go 'x := 1'
printf 'internal/a/a_test.go\ninternal/b/b_test.go\ninternal/c/c_test.go\ninternal/gone/gone_test.go\n' >"$root/backlog.txt"
run
assert_eq "$rc" "1" "stale backlog: exit 1"
assert_contains "$out" "STALE       internal/b/b_test.go: on the backlog but it attaches an engine now" "stale: engine now"
assert_contains "$out" "STALE       internal/c/c_test.go: on the backlog but it no longer builds api.New" "stale: no api.New"
assert_contains "$out" "STALE       internal/gone/gone_test.go: on the backlog but the file is gone" "stale: gone"
assert_not_contains "$out" "internal/a/a_test.go" "stale run: live entry not reported"

# 6. An unreadable backlog is exit 2.
fresh nobacklog
rm "$root/backlog.txt"
run
assert_eq "$rc" "2" "missing backlog: exit 2"

# 7. The real tree passes.
set +e
real_out="$(bash "$SCRIPT" 2>&1)"
real_rc=$?
set -e
assert_eq "$real_rc" "0" "real tree: exit 0 ($real_out)"

echo "check-harness-engine_test: $pass passed, $fail failed"
if ((fail > 0)); then
  printf '%s\n' "${fail_messages[@]}"
  exit 1
fi
