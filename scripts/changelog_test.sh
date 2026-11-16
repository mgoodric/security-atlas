#!/usr/bin/env bash
#
# changelog_test.sh — #2357 self-test for scripts/changelog.sh.
#
# Synthetic fixtures plus a real-tree `check`.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SCRIPT_DIR/changelog.sh"
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

run() {
  # run <dir> <args...>: exit code and combined output in $rc / $out.
  local dir="$1"
  shift
  set +e
  out="$(CHANGELOG_DIR="$dir" CHANGELOG_FILE="$tmp/CHANGELOG.md" bash "$SCRIPT" "$@" 2>&1)"
  rc=$?
  set -e
}

# --- check ---------------------------------------------------------------

good="$tmp/good"
mkdir -p "$good"
printf '%s\n' '- older fix (#90).' >"$good/90.fixed.md"
printf '%s\n' '- newer fix (#1200).' '' '' >"$good/1200.fixed.md"
printf '%s\n' '- second fix from 1200.' >"$good/1200-2.fixed.md"
printf '%s\n' '- slice fix.' >"$good/slice-7.fixed.md"
printf '%s\n' '- a feature (#5).' '  continued line.' >"$good/5.added.md"
printf '%s\n' '# not a fragment' >"$good/README.md"

run "$good" check
assert_eq "$rc" "0" "check passes on valid fragments (README ignored)"

bad="$tmp/bad"
mkdir -p "$bad"
printf '%s\n' '- x' >"$bad/12.feature.md"
run "$bad" check
assert_eq "$rc" "1" "check fails on an unknown type"
assert_contains "$out" "12.feature.md" "check names the bad file"

rm "$bad/12.feature.md"
printf '%s\n' 'prose, not a bullet' >"$bad/12.fixed.md"
run "$bad" check
assert_eq "$rc" "1" "check fails on a body that is not a bullet"

rm "$bad/12.fixed.md"
printf '%s\n' '- x' >"$bad/Upper.fixed.md"
run "$bad" check
assert_eq "$rc" "1" "check fails on an uppercase name"

# --- render ----------------------------------------------------------------

run "$good" render
want='### Added

- a feature (#5).
  continued line.

### Fixed

- newer fix (#1200).

- second fix from 1200.

- older fix (#90).

- slice fix.'
assert_eq "$rc" "0" "render exits 0"
assert_eq "$out" "$want" "render orders sections by type and entries newest first"

run "$tmp/empty" render
assert_eq "$rc" "0" "render with no directory exits 0"
assert_eq "$out" "" "render with no directory prints nothing"

# --- assemble --------------------------------------------------------------

asm="$tmp/asm"
cp -R "$good" "$asm"
cat >"$tmp/CHANGELOG.md" <<'EOF'
# Changelog

## [1.2.0](https://example.test) (2026-11-15)

### Features

* existing release-please entry

## [1.1.0](https://example.test) (2026-10-01)
EOF

run "$asm" assemble 9.9.9
assert_eq "$rc" "1" "assemble refuses a version with no heading"

run "$asm" assemble
assert_eq "$rc" "2" "assemble requires a version"

run "$asm" assemble 1.2.0
assert_eq "$rc" "0" "assemble exits 0"
got="$(cat "$tmp/CHANGELOG.md")"
want='# Changelog

## [1.2.0](https://example.test) (2026-11-15)

### Added

- a feature (#5).
  continued line.

### Fixed

- newer fix (#1200).

- second fix from 1200.

- older fix (#90).

- slice fix.

### Features

* existing release-please entry

## [1.1.0](https://example.test) (2026-10-01)'
assert_eq "$got" "$want" "assemble writes the sections under the release heading"
assert_eq "$(find "$asm" -type f | sed 's|.*/||' | sort | tr '\n' ' ')" "README.md " \
  "assemble deletes the fragments and keeps the README"

# --- require ---------------------------------------------------------------

req() {
  # req <stdin-files> [env...]
  local files="$1"
  shift
  set +e
  out="$(printf '%s' "$files" | env CHANGELOG_DIR=changelog.d "$@" bash "$SCRIPT" require 2>&1)"
  rc=$?
  set -e
}

req $'internal/api/x.go\nchangelog.d/2357.fixed.md\n'
assert_eq "$rc" "0" "require passes a code PR with a fragment"

req $'internal/api/x.go\nweb/app/page.tsx\n'
assert_eq "$rc" "1" "require fails a code PR without a fragment"
assert_contains "$out" "no-changelog" "the failure names the opt-out label"

req $'internal/api/x.go\nchangelog.d/README.md\n'
assert_eq "$rc" "1" "editing the README is not a fragment"

req $'docs/x.md\nPlans/canvas/01-vision.md\ndocs-site/docs/a.png\nisa/probes/p.sh\nREADME.md\n'
assert_eq "$rc" "0" "require exempts a docs-only PR"

req $'internal/api/x.go\n' PR_LABELS=$'bug\nno-changelog'
assert_eq "$rc" "0" "require exempts the no-changelog label"

req $'go.mod\ngo.sum\n' 'PR_AUTHOR=dependabot[bot]'
assert_eq "$rc" "0" "require exempts Dependabot"

req $'CHANGELOG.md\n.release-please-manifest.json\n' PR_HEAD_REF=release-please--branches--main
assert_eq "$rc" "0" "require exempts the release-please PR"

req ''
assert_eq "$rc" "0" "require passes an empty change set"

# --- real tree ---------------------------------------------------------------

set +e
out="$(cd "$REPO_ROOT" && bash "$SCRIPT" check 2>&1)"
rc=$?
set -e
assert_eq "$rc" "0" "every fragment in the real tree is valid${out:+: $out}"

echo "changelog_test: $pass passed, $fail failed"
if [[ $fail -gt 0 ]]; then
  printf '%s\n' "${fail_messages[@]}" >&2
  exit 1
fi
