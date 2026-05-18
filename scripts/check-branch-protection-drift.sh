#!/usr/bin/env bash
#
# check-branch-protection-drift.sh — compare the required-checks list in
# `.github/branch-protection.json` against the live GitHub branch-
# protection config on `main` and exit non-zero on any drift.
#
# Slice 127 — this is the local-repro script for AC-7 and the worker
# that the `branch-protection-drift` CI job in `.github/workflows/ci.yml`
# invokes (AC-3 + AC-5). Both surfaces use the same comparison logic so
# contributors can reproduce a CI finding by running this script
# locally.
#
# Why it exists:
#   `.github/branch-protection.json` is the file-as-source-of-truth for
#   the branch-protection ruleset on `main`. The file is applied to live
#   by `scripts/apply-branch-protection.sh`. If a maintainer edits the
#   file but forgets the apply (or applies a change via `gh api` without
#   updating the file), the two sides silently drift apart. Drift
#   surfaced in this exact way during the 2026-05-17/18 cascade-unblock
#   session — four PRs sat held for hours on a phantom-blocker rationale
#   (`Frontend · Playwright e2e is failing`) that turned out to be a
#   check NOT enforced live. Slice 127 makes the next drift event visible
#   instead of waiting hours of debugging.
#
# Scope: this script compares ONLY the
# `required_status_checks.contexts` list. Other fields (`enforce_admins`,
# `required_pull_request_reviews`, `required_linear_history`,
# `allow_force_pushes`, etc.) are NOT compared in v1 — the contexts list
# is where day-to-day drift accumulates and where the 2026-05-18 incident
# fired. A follow-up slice can extend coverage to the full ruleset if
# drift surfaces in other fields.
#
# Output:
#   - On success (no drift): one line "no drift detected" + exit 0.
#   - On drift detected: prints the unified diff between the two sorted
#     context lists (file on the left, live on the right) + exit 1.
#
# Env:
#   ATLAS_REPO           Override the repo path (default:
#                        mgoodric/security-atlas).
#   ATLAS_BRANCH         Override the branch (default: main).
#   ATLAS_FIXTURE_FILE   Override the file the script reads as the
#                        "file" side. Used by the test harness to point
#                        at a synthetic fixture instead of the real
#                        in-tree file.
#   ATLAS_FIXTURE_LIVE   Override the live side: read the live contexts
#                        from this file instead of calling `gh api`.
#                        Used by the test harness to fake a live
#                        response without touching the network.
#
# Required:
#   - `jq` on PATH (always)
#   - `gh` CLI authenticated against an account with repo:read on the
#     repo (only when ATLAS_FIXTURE_LIVE is NOT set)
#   - `diff` on PATH (every POSIX system has it)
#
# Exit codes:
#   0 — no drift detected
#   1 — drift detected (one or more contexts differ between file and live)
#   2 — environment misconfigured (missing tool, malformed file, missing
#       required-fields)

set -Eeuo pipefail

REPO="${ATLAS_REPO:-mgoodric/security-atlas}"
BRANCH="${ATLAS_BRANCH:-main}"

# Resolve the in-tree file (unless overridden by the fixture env).
if [[ -n "${ATLAS_FIXTURE_FILE:-}" ]]; then
  FILE="$ATLAS_FIXTURE_FILE"
else
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
  FILE="$ROOT/.github/branch-protection.json"
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "check-branch-protection-drift: missing required tool 'jq' on PATH" >&2
  exit 2
fi

if [[ ! -f "$FILE" ]]; then
  echo "check-branch-protection-drift: cannot find file at $FILE" >&2
  exit 2
fi

if ! jq -e . "$FILE" >/dev/null 2>&1; then
  echo "check-branch-protection-drift: $FILE is not valid JSON" >&2
  exit 2
fi

# File side: read `.required_status_checks.contexts`, sort, emit
# compact JSON. We use `jq -cS` so the byte-equal compare is robust
# against insertion order and pretty-print whitespace.
#
# Guard the missing-list case BEFORE the `| sort` pipeline — `null |
# sort` errors out in jq (null is not an array), so a missing list
# would otherwise surface as a confusing jq error instead of our own
# diagnostic. The `// empty` filter emits nothing when the key is
# missing, which we detect as an empty `file_ctx` string.
file_ctx="$(jq -cS '.required_status_checks.contexts // empty | sort' "$FILE")"

if [[ -z "$file_ctx" ]]; then
  echo "check-branch-protection-drift: $FILE has no .required_status_checks.contexts list — cannot compare" >&2
  exit 2
fi

# Live side: either from the fixture file (test harness) or via `gh api`.
if [[ -n "${ATLAS_FIXTURE_LIVE:-}" ]]; then
  if [[ ! -f "$ATLAS_FIXTURE_LIVE" ]]; then
    echo "check-branch-protection-drift: ATLAS_FIXTURE_LIVE set but file missing: $ATLAS_FIXTURE_LIVE" >&2
    exit 2
  fi
  live_ctx="$(jq -cS '. | sort' "$ATLAS_FIXTURE_LIVE")"
else
  if ! command -v gh >/dev/null 2>&1; then
    echo "check-branch-protection-drift: missing required tool 'gh' on PATH (or set ATLAS_FIXTURE_LIVE to bypass)" >&2
    exit 2
  fi
  if ! live_ctx="$(gh api "repos/${REPO}/branches/${BRANCH}/protection/required_status_checks" --jq '.contexts | sort' 2>/dev/null)"; then
    echo "check-branch-protection-drift: gh api call failed (repos/${REPO}/branches/${BRANCH}/protection/required_status_checks)" >&2
    exit 2
  fi
  # `gh api --jq` emits compact JSON already, but normalize through jq
  # for byte-equal parity with the file side.
  live_ctx="$(echo "$live_ctx" | jq -cS '. | sort')"
fi

if [[ "$file_ctx" == "$live_ctx" ]]; then
  echo "check-branch-protection-drift: no drift detected — file ↔ live in sync (${file_ctx})"
  exit 0
fi

# Drift detected — emit a human-readable diff on stderr (so callers
# capturing stdout get a clean exit-1 signal) and a brief reconcile
# hint.
echo "check-branch-protection-drift: drift detected between .github/branch-protection.json and live config on '${BRANCH}'" >&2
echo "" >&2
echo "--- file (.github/branch-protection.json)" >&2
echo "+++ live (gh api repos/${REPO}/branches/${BRANCH}/protection/required_status_checks)" >&2
diff <(echo "$file_ctx" | jq -r '.[]') <(echo "$live_ctx" | jq -r '.[]') >&2 || true
echo "" >&2
echo "To reconcile, decide which side is correct, then either:" >&2
echo "  (a) Edit .github/branch-protection.json to match live, OR" >&2
echo "  (b) Run scripts/apply-branch-protection.sh to push the file to live." >&2
exit 1
