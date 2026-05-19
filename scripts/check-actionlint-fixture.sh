#!/usr/bin/env bash
#
# check-actionlint-fixture.sh — slice 158 AC-17 smoke test.
#
# Asserts that the slice-158 actionlint gate would catch the exact
# `administration: read` mistake PR #311 made. The fixture lives at
# `scripts/actionlint-fixture-invalid-scope.yml` and intentionally
# carries an invalid GITHUB_TOKEN permission scope; running actionlint
# against it MUST exit non-zero with an `unknown permission scope`
# diagnostic.
#
# This script is the load-bearing test for the slice-158 guard. If
# actionlint upstream ever stops flagging the `administration` scope
# (e.g. they add it as a valid scope someday), this test will start
# passing where it should fail — which would surface as a new red
# CHECK in the `pre-commit · all hooks` CI job + locally in pre-commit,
# prompting a follow-up slice to pick a still-invalid scope name as
# the canary.
#
# Run: bash scripts/check-actionlint-fixture.sh
# Exit codes:
#   0 — actionlint correctly flagged the fixture (guard intact)
#   1 — actionlint did NOT flag the fixture (guard silently broken)
#   2 — environment misconfigured (actionlint not installed)

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FIXTURE="$SCRIPT_DIR/actionlint-fixture-invalid-scope.yml"

if ! command -v actionlint >/dev/null 2>&1; then
  echo "check-actionlint-fixture: missing required tool 'actionlint' on PATH" >&2
  echo "  install via: brew install actionlint  (macOS)" >&2
  echo "  or download from: https://github.com/rhysd/actionlint/releases" >&2
  exit 2
fi

if [[ ! -f "$FIXTURE" ]]; then
  echo "check-actionlint-fixture: fixture missing at $FIXTURE" >&2
  exit 2
fi

# Run actionlint with shellcheck disabled — we only care about the
# permission-scope diagnostic, not pre-existing shellcheck nits. The
# `-shellcheck ""` flag mirrors the pre-commit hook + CI invocation so
# all three surfaces produce identical output for the fixture.
set +e
out="$(actionlint -shellcheck "" -no-color "$FIXTURE" 2>&1)"
rc=$?
set -e

if [[ $rc -eq 0 ]]; then
  echo "check-actionlint-fixture: FAIL — actionlint did not flag the invalid scope" >&2
  echo "  fixture: $FIXTURE" >&2
  echo "  actionlint output:" >&2
  echo "$out" >&2
  exit 1
fi

# Confirm the diagnostic actually names the scope problem (not some
# other unrelated lint that happened to fire).
if ! grep -qE 'unknown permission scope|"administration"' <<<"$out"; then
  echo "check-actionlint-fixture: FAIL — actionlint exited non-zero but did NOT mention the invalid scope" >&2
  echo "  actionlint output:" >&2
  echo "$out" >&2
  exit 1
fi

echo "check-actionlint-fixture: PASS — actionlint correctly flagged the invalid 'administration' scope"
exit 0
