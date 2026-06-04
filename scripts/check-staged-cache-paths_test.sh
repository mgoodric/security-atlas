#!/usr/bin/env bash
#
# Smoke tests for scripts/check-staged-cache-paths.sh.
#
# Slice 458 — integration test exercising the guard against synthetic
# fixture repos (built with `git init` in a tmpdir) and explicit path
# lists. Covers AC-1 (fires on a staged forbidden path), AC-3 (no false
# positive on a clean tree / legitimately-tracked lookalikes), and the
# three invocation modes (--staged / --all-tracked / --args).
#
# The harness uses the ATLAS_CACHE_GUARD_ROOT env override (and the
# `--args` mode) so the script never touches the real in-tree git index.
# Runnable in offline sandboxes.
#
# Run: bash scripts/check-staged-cache-paths_test.sh
# Exits non-zero on first failed assertion summary.

set -eu

SCRIPT="$(cd "$(dirname "$0")" && pwd)/check-staged-cache-paths.sh"
if [[ ! -x "$SCRIPT" ]]; then
  echo "check-staged-cache-paths_test: script not executable at $SCRIPT" >&2
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
# Helper: build a throwaway git repo, stage the given paths.
# --------------------------------------------------------------------
make_repo() {
  local dir
  dir="$(mktemp -d)"
  git -C "$dir" init -q
  git -C "$dir" config user.email t@t.test
  git -C "$dir" config user.name test
  echo "$dir"
}

# --------------------------------------------------------------------
# Case 1 — staged forbidden path (.understand-anything/) — exit 1 (AC-1)
# --------------------------------------------------------------------
repo1="$(make_repo)"
mkdir -p "$repo1/.understand-anything"
echo '{}' >"$repo1/.understand-anything/knowledge-graph.json"
echo "real" >"$repo1/README.md"
git -C "$repo1" add -A
set +e
out1="$(ATLAS_CACHE_GUARD_ROOT="$repo1" "$SCRIPT" --staged 2>&1)"
rc1=$?
set -e
assert_eq "$rc1" "1" "case 1 (AC-1): staged .understand-anything/ should exit 1"
assert_contains "$out1" ".understand-anything/knowledge-graph.json" "case 1: names the offending staged path"
assert_contains "$out1" "machine-local cache" "case 1: actionable message mentions machine-local cache"
assert_contains "$out1" ".gitignore" "case 1: message points at .gitignore as the fix"

# --------------------------------------------------------------------
# Case 2 — clean staging (only legitimate source) — exit 0 (AC-3)
# --------------------------------------------------------------------
repo2="$(make_repo)"
mkdir -p "$repo2/internal/api"
echo "package api" >"$repo2/internal/api/api.go"
echo "real" >"$repo2/README.md"
git -C "$repo2" add -A
set +e
out2="$(ATLAS_CACHE_GUARD_ROOT="$repo2" "$SCRIPT" --staged 2>&1)"
rc2=$?
set -e
assert_eq "$rc2" "0" "case 2 (AC-3): clean staging should exit 0"
assert_contains "$out2" "clean" "case 2: success line says clean"

# --------------------------------------------------------------------
# Case 3 — false-positive guard: lookalike paths that must NOT fire (AC-3)
#   `.cache` as a substring of a real segment, a tracked .vscode-like
#   filename that is not the dir, etc.
# --------------------------------------------------------------------
repo3="$(make_repo)"
mkdir -p "$repo3/web/app"
echo "// warmer" >"$repo3/web/app/.cache-warmer.ts" # .cache- is NOT segment .cache
echo "x" >"$repo3/cache.md"                         # 'cache' without leading dot
echo "x" >"$repo3/node_modules.md"                  # filename, not the dir
git -C "$repo3" add -A
set +e
out3="$(ATLAS_CACHE_GUARD_ROOT="$repo3" "$SCRIPT" --staged 2>&1)"
rc3=$?
set -e
assert_eq "$rc3" "0" "case 3 (AC-3): cache-lookalike non-cache paths must NOT fire"

# --------------------------------------------------------------------
# Case 4 — nested cache dir (web/.next/) — exit 1
# --------------------------------------------------------------------
repo4="$(make_repo)"
mkdir -p "$repo4/web/.next/cache"
echo "x" >"$repo4/web/.next/cache/blob"
git -C "$repo4" add -A
set +e
out4="$(ATLAS_CACHE_GUARD_ROOT="$repo4" "$SCRIPT" --staged 2>&1)"
rc4=$?
set -e
assert_eq "$rc4" "1" "case 4: nested .next/ cache should exit 1"
assert_contains "$out4" "web/.next/cache/blob" "case 4: names the nested offending path"

# --------------------------------------------------------------------
# Case 5 — .DS_Store basename match — exit 1
# --------------------------------------------------------------------
repo5="$(make_repo)"
mkdir -p "$repo5/docs"
echo "x" >"$repo5/docs/.DS_Store"
git -C "$repo5" add -A
set +e
out5="$(ATLAS_CACHE_GUARD_ROOT="$repo5" "$SCRIPT" --staged 2>&1)"
rc5=$?
set -e
assert_eq "$rc5" "1" "case 5: staged .DS_Store should exit 1"
assert_contains "$out5" ".DS_Store" "case 5: names the .DS_Store path"

# --------------------------------------------------------------------
# Case 6 — --args mode (pre-commit pass_filenames path): forbidden — exit 1
# --------------------------------------------------------------------
set +e
out6="$("$SCRIPT" --args internal/api/api.go .understand-anything/meta.json 2>&1)"
rc6=$?
set -e
assert_eq "$rc6" "1" "case 6 (AC-1 via pre-commit argv): forbidden path in argv should exit 1"
assert_contains "$out6" ".understand-anything/meta.json" "case 6: names the argv offending path"

# --------------------------------------------------------------------
# Case 7 — --args mode, all clean — exit 0 (AC-3)
# --------------------------------------------------------------------
set +e
out7="$("$SCRIPT" --args internal/api/api.go web/app/page.tsx README.md 2>&1)"
rc7=$?
set -e
assert_eq "$rc7" "0" "case 7 (AC-3 via pre-commit argv): clean argv should exit 0"

# --------------------------------------------------------------------
# Case 8 — bare-path argv (no flag) is treated as the explicit list too
#   (pre-commit appends filenames with no flag).
# --------------------------------------------------------------------
set +e
out8="$("$SCRIPT" internal/api/api.go .understand-anything/x.json 2>&1)"
rc8=$?
set -e
assert_eq "$rc8" "1" "case 8: bare-path argv with a forbidden path should exit 1"

# --------------------------------------------------------------------
# Case 9 — --all-tracked mode (CI surface, AC-2): a committed cache is
#   caught even though it is no longer staged.
# --------------------------------------------------------------------
repo9="$(make_repo)"
mkdir -p "$repo9/.understand-anything"
echo "x" >"$repo9/.understand-anything/fingerprints.json"
echo "real" >"$repo9/README.md"
git -C "$repo9" add -A
git -C "$repo9" commit -q -m "oops committed a cache"
set +e
out9="$(ATLAS_CACHE_GUARD_ROOT="$repo9" "$SCRIPT" --all-tracked 2>&1)"
rc9=$?
set -e
assert_eq "$rc9" "1" "case 9 (AC-2): --all-tracked catches a committed cache"
assert_contains "$out9" ".understand-anything/fingerprints.json" "case 9: names the tracked cache path"

# --------------------------------------------------------------------
# Case 10 — reproducibility: re-run case 2 — still clean
# --------------------------------------------------------------------
set +e
ATLAS_CACHE_GUARD_ROOT="$repo2" "$SCRIPT" --staged >/dev/null 2>&1
rc10=$?
set -e
assert_eq "$rc10" "0" "case 10: reproducibility — re-run of clean repo still exits 0"

# Cleanup tmp repos.
rm -rf "$repo1" "$repo2" "$repo3" "$repo4" "$repo5" "$repo9"

# --------------------------------------------------------------------
# Report
# --------------------------------------------------------------------
echo ""
echo "check-staged-cache-paths_test: $pass passed, $fail failed"
if ((fail > 0)); then
  for msg in "${fail_messages[@]}"; do
    echo "  $msg"
  done
  exit 1
fi
