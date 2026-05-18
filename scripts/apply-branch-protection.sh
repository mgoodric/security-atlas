#!/usr/bin/env bash
#
# apply-branch-protection.sh — push the in-tree branch-protection
# config to the live GitHub branch-protection ruleset on `main`.
#
# Slice 127 — this is the apply-ritual referenced by AC-4 + AC-9. Without
# this script (or its equivalent `gh api` invocation) the file-as-source-
# of-truth claim of `.github/branch-protection.json` is structurally
# untrue: a maintainer who edits the file but does not apply has produced
# silent drift between the file (intent) and the live config
# (enforcement). Drift surfaced in this exact way during the 2026-05-18
# cascade-unblock session — see slice 127 narrative.
#
# When to run:
#   - After merging any PR that edits `.github/branch-protection.json`.
#   - After the `branch-protection-drift` informational CI job posts a
#     sticky comment saying "file vs live differs" — once the maintainer
#     has decided which side is correct.
#   - After adding a new required-checks-eligible workflow job whose
#     name the file already lists (i.e. once the job has run on `main`
#     and reported a status under that name at least once).
#
# What it does:
#   1. Reads `.github/branch-protection.json` from the repo root
#      (resolved via the script's own location, so `cd` is unnecessary).
#   2. Validates the file is well-formed JSON via `jq`.
#   3. Strips the `$comment`, `$deviations_*`, `$verification`, and
#      `$rationale_*` keys (GitHub's PUT API rejects unknown top-level
#      fields; the `$`-prefixed keys are this repo's annotation
#      convention, not GitHub's wire format).
#   4. PUTs the cleaned payload to
#      `/repos/mgoodric/security-atlas/branches/main/protection` via
#      `gh api`.
#   5. Re-reads the live config and runs the same byte-equal diff that
#      the drift-detect script uses — exits non-zero if the apply did
#      not converge.
#
# Idempotency: running this script twice in a row produces identical
# state on the second run (P0-A2). The `gh api PUT` is a full-replace
# operation, so it is idempotent by construction — re-applying the same
# payload makes no observable change.
#
# Required:
#   - `gh` CLI authenticated against an account with admin:repo on
#     mgoodric/security-atlas.
#   - `jq` on PATH.
#
# Env overrides:
#   ATLAS_REPO         Override the repo path (default: mgoodric/security-atlas).
#                      Useful for testing against a fork or sandbox.
#   ATLAS_BRANCH       Override the branch (default: main). The file
#                      currently protects `main` only; this exists for
#                      future-proofing.
#   DRY_RUN            If set to any non-empty value, print the payload
#                      that would be sent and exit 0 without calling
#                      the API. For local sanity-checking before push.
#
# Exit codes:
#   0 — apply succeeded AND post-apply diff is empty
#   1 — environment misconfigured (missing tool, malformed file)
#   2 — API call failed
#   3 — apply call succeeded but post-apply diff is non-empty (i.e. the
#       file lists contexts the live API silently dropped — usually
#       because GitHub does not know the context name yet because the
#       check has never reported on `main`)

set -Eeuo pipefail

REPO="${ATLAS_REPO:-mgoodric/security-atlas}"
BRANCH="${ATLAS_BRANCH:-main}"

# Resolve the repo root from the script's own location so callers can
# invoke this from any cwd.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
FILE="$ROOT/.github/branch-protection.json"

for tool in gh jq; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "apply-branch-protection: missing required tool '$tool' on PATH" >&2
    exit 1
  fi
done

if [[ ! -f "$FILE" ]]; then
  echo "apply-branch-protection: cannot find $FILE" >&2
  exit 1
fi

# Validate the file is well-formed JSON.
if ! jq -e . "$FILE" >/dev/null 2>&1; then
  echo "apply-branch-protection: $FILE is not valid JSON" >&2
  exit 1
fi

# Strip `$`-prefixed annotation keys from the top level of the payload.
# Those are this repo's documentation convention; the GitHub PUT API
# rejects unknown top-level keys. The keys we strip:
#   $comment, $deviations_from_slice_050_AC11, $deviations_from_slice_069,
#   $rationale_required_signatures_off, $verification
# We delete ALL keys starting with `$` to keep the rule simple — any
# future annotation key prefixed with `$` is treated as documentation.
payload="$(jq 'with_entries(select(.key | startswith("$") | not))' "$FILE")"

if [[ -n "${DRY_RUN:-}" ]]; then
  echo "DRY_RUN — would PUT to repos/${REPO}/branches/${BRANCH}/protection with payload:"
  echo "$payload"
  exit 0
fi

echo "apply-branch-protection: PUT repos/${REPO}/branches/${BRANCH}/protection"

# Pipe the cleaned payload to `gh api PUT`. Use `--input -` so gh reads
# the body from stdin (avoids the 32KB ARG_MAX cap that -f flags would
# inflict on large payloads).
if ! echo "$payload" | gh api -X PUT "repos/${REPO}/branches/${BRANCH}/protection" --input - >/dev/null; then
  echo "apply-branch-protection: gh api PUT failed" >&2
  exit 2
fi

echo "apply-branch-protection: PUT succeeded — verifying convergence..."

# Convergence check — re-read the live config and assert the
# required_status_checks.contexts list matches the file. This catches
# the documented edge case where GitHub silently drops a context name
# the file lists but the API does not yet know (because the named
# check has never reported a status on `main`).
file_ctx="$(jq -cS '.required_status_checks.contexts | sort' "$FILE")"
live_ctx="$(gh api "repos/${REPO}/branches/${BRANCH}/protection/required_status_checks" --jq '.contexts | sort')"

if [[ "$file_ctx" == "$live_ctx" ]]; then
  echo "apply-branch-protection: converged — file ↔ live in sync."
  exit 0
fi

echo "apply-branch-protection: post-apply diff non-empty:" >&2
diff <(echo "$file_ctx") <(echo "$live_ctx") >&2 || true
echo "apply-branch-protection: this usually means GitHub silently dropped a context name the file lists but the API does not yet know (the named check has never reported on '${BRANCH}'). Trigger one CI run that produces that check, then re-run this script." >&2
exit 3
