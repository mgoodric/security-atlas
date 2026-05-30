#!/usr/bin/env bash
#
# audit-integration-enrolment.sh — slice 345
#
# Assert that every Go package carrying a `//go:build integration` build
# tag is ENROLLED in the integration job's explicit package list in
# `.github/workflows/ci.yml`.
#
# Background (slice 334 framework audit, finding I-1): the Go integration
# job enrols packages by EXPLICIT listing — a curated set of
# `./internal/<pkg>/...` entries under the "Run integration tests" step.
# A package that ships an `integration_test.go` (i.e. a `_test.go` file
# carrying `//go:build integration`) but is NOT in that list silently
# runs no integration tests in CI. Its coverage is unit-only and any RLS
# / real-services bug its integration suite would catch goes unnoticed.
#
# The cost is visible in the 17-slice retroactive-enrolment trail
# (slices 279, 283, 284, 287, 288, 290, 293, 294, 295, 297, 310, 313,
# 315, 317, 318, 319, 320) — each enrolled a package whose
# integration_test.go shipped earlier and was forgotten. This script is
# the structural fix: it fails CI when the gap reopens.
#
# Shape: option (1) from the slice doc — a standalone shell script
# matching the `scripts/audit-rls.sh` precedent. Greps the repo for the
# build tag, derives the set of package directories, diffs against the
# parsed yaml list, and exits non-zero naming any missing package.
#
# Known-gaps allowlist (KNOWN_UNENROLLED)
# ----------------------------------------
# When this guard was authored (2026-05-29) the tree already carried a
# 38-package enrolment backlog — packages tagged for integration whose
# tests have never run in CI. Slice 345's anti-criterion P0-345-1
# forbids retroactively enrolling them here (that is separate enrolment
# work — slice 390). To make the guard pass on the current tree while
# still catching the *next* forgotten package, the 38 are recorded in
# KNOWN_UNENROLLED below as a documented, dated waiver. The list is a
# RATCHET: it must only ever shrink. Draining it is slice 390's job —
# each enrolment PR removes its package from BOTH the ci.yml list (adds)
# and this allowlist (removes). A package that is neither enrolled nor
# waived fails the guard. Adding a new entry to KNOWN_UNENROLLED is a
# code smell that requires explicit justification in the PR.
#
# Exit codes:
#   0 — every tagged package is enrolled OR explicitly waived
#   1 — at least one tagged package is neither enrolled nor waived
#   2 — environment misconfigured (not in repo root, ci.yml unreadable,
#       no package list found, allowlist references a no-longer-tagged
#       package)
#
# Usage:
#   scripts/audit-integration-enrolment.sh           # audit the repo
#   AUDIT_ENROL_GREP_ROOT=path/to/tree scripts/...    # test override
#   AUDIT_ENROL_CI_YML=path/to/ci.yml scripts/...     # test override
#
# Run by `just audit-integration-enrolment` and by CI on every PR.

set -Eeuo pipefail

# --------------------------------------------------------------------
# Resolve paths. Default to the repo containing this script so the audit
# works from any CWD. The two AUDIT_ENROL_* overrides exist purely for
# the companion test harness (audit-integration-enrolment_test.sh), which
# points them at synthetic fixture trees to exercise the pass/fail paths.
# --------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

GREP_ROOT="${AUDIT_ENROL_GREP_ROOT:-$REPO_ROOT/internal}"
CI_YML="${AUDIT_ENROL_CI_YML:-$REPO_ROOT/.github/workflows/ci.yml}"

# --------------------------------------------------------------------
# Known-gaps allowlist. One package path per line, relative to the repo
# root, exactly as it would appear in the ci.yml list MINUS the trailing
# `/...`. RATCHET: shrink only. Drain via slice 390.
#
# Provenance: the 38 packages tagged `//go:build integration` but absent
# from the ci.yml integration list as of 2026-05-29. Catalogued
# independently by slice 348 (docs/audits/348-coverage-excludes-audit.md,
# category (c) TEST_PRESENT).
# --------------------------------------------------------------------
read -r -d '' KNOWN_UNENROLLED <<'WAIVED' || true
internal/api/emptyset
internal/api/freshnessdrift
internal/api/questionnaires
internal/api/ucfcoverage
internal/audit/notes
internal/auth
internal/auth/keystore/fsstore
internal/catalog/metrics
internal/drift
internal/exception
internal/freshness
internal/freshnessdrift
internal/mcp
internal/observability/otel
internal/oscal
internal/policy/pdf
internal/policy/seed
internal/risk/aggrule
WAIVED

if [[ ! -d "$GREP_ROOT" ]]; then
  echo "audit-integration-enrolment: grep root not a directory: $GREP_ROOT" >&2
  exit 2
fi
if [[ ! -r "$CI_YML" ]]; then
  echo "audit-integration-enrolment: ci.yml not readable: $CI_YML" >&2
  exit 2
fi

# --------------------------------------------------------------------
# 1. Tagged set: directories under GREP_ROOT containing at least one file
#    with a `//go:build integration` build tag. `grep -rl` lists files;
#    dirname + sort -u collapses to the package directory set. Paths are
#    normalized to be relative to the repo root so they line up with both
#    the ci.yml list and the allowlist.
# --------------------------------------------------------------------
tagged_tmp="$(mktemp)"
listed_tmp="$(mktemp)"
waived_tmp="$(mktemp)"
trap 'rm -f "$tagged_tmp" "$listed_tmp" "$waived_tmp"' EXIT

# grep -r returns non-zero when there are zero matches; tolerate it.
if ! grep -rl --include='*.go' '//go:build integration' "$GREP_ROOT" \
  > "$tagged_tmp.files" 2>/dev/null; then
  : > "$tagged_tmp.files"
fi

# Map matched files -> package dirs, normalized to an `internal/...`
# suffix. Both the real repo and the test fixtures place packages under
# an `internal/` directory; keying on that segment makes the comparison
# robust to absolute fixture paths (test mode) and to the repo root
# (real mode) alike. The ci.yml list and the allowlist are both in the
# same `internal/<pkg>` shape, so the three sets line up.
: > "$tagged_tmp"
while IFS= read -r f; do
  [[ -z "$f" ]] && continue
  d="$(dirname "$f")"
  # Strip everything up to and including the LAST `internal/` so the
  # path collapses to `internal/<pkg...>`.
  case "$d" in
    */internal/*) d="internal/${d##*/internal/}" ;;
    */internal)   d="internal" ;;
    internal/*|internal) : ;;
  esac
  echo "$d"
done < "$tagged_tmp.files" | sort -u > "$tagged_tmp"
rm -f "$tagged_tmp.files"

# --------------------------------------------------------------------
# 2. Listed set: the `./internal/<pkg>/...` entries enumerated in the
#    "Run integration tests" `go test` invocation of ci.yml. We extract
#    every `./internal/...` token, strip the leading `./` and trailing
#    `/...`, and sort-unique. This is robust to line reordering and to
#    the list growing; it keys purely on the token shape.
# --------------------------------------------------------------------
# `grep` exits 1 on zero matches; under `pipefail` that would abort the
# script before the emptiness check below. Tolerate it so the explicit
# "no package list" diagnostic (exit 2) wins.
{ grep -oE '\./internal/[A-Za-z0-9_/]+(/\.\.\.)?' "$CI_YML" || true; } \
  | sed -E 's|^\./||; s|/\.\.\.$||' \
  | sort -u > "$listed_tmp"

if [[ ! -s "$listed_tmp" ]]; then
  echo "audit-integration-enrolment: found no ./internal/... package entries in $CI_YML" >&2
  echo "  (expected the integration job's explicit package list)" >&2
  exit 2
fi

# --------------------------------------------------------------------
# 3. Waived set: the allowlist, sorted-unique, blank lines dropped.
# --------------------------------------------------------------------
printf '%s\n' "$KNOWN_UNENROLLED" | sed '/^[[:space:]]*$/d' | sort -u > "$waived_tmp"

# --------------------------------------------------------------------
# 3b. Allowlist hygiene: every waived package MUST still carry the tag.
#     A waiver for a package that no longer has an integration test is
#     stale — it should have been removed when the package was enrolled
#     or the test deleted. Fail loud (exit 2) so the ratchet cannot rot.
#
#     Only enforced when auditing the REAL repo. Under the test
#     overrides the grep root is a synthetic fixture tree that does not
#     contain the real allowlist's packages, so the hygiene check would
#     spuriously fire; the fixtures exercise the enrolment gate proper,
#     not the allowlist's provenance.
# --------------------------------------------------------------------
TEST_MODE=false
if [[ -n "${AUDIT_ENROL_GREP_ROOT:-}" || -n "${AUDIT_ENROL_CI_YML:-}" ]]; then
  TEST_MODE=true
fi

stale_waivers="$(comm -23 "$waived_tmp" "$tagged_tmp")"
if [[ "$TEST_MODE" == "false" && -n "$stale_waivers" ]]; then
  echo "audit-integration-enrolment: KNOWN_UNENROLLED has STALE entries" >&2
  echo "  (these packages no longer carry a //go:build integration tag; remove them from the allowlist):" >&2
  while IFS= read -r p; do
    [[ -z "$p" ]] && continue
    echo "    - $p" >&2
  done <<<"$stale_waivers"
  exit 2
fi

# --------------------------------------------------------------------
# 4. The gate: tagged - listed - waived. Any remainder is a forgotten
#    package — an integration suite that ships but never runs in CI.
# --------------------------------------------------------------------
enrolled_or_waived="$(mktemp)"
cat "$listed_tmp" "$waived_tmp" | sort -u > "$enrolled_or_waived"
missing="$(comm -23 "$tagged_tmp" "$enrolled_or_waived")"
rm -f "$enrolled_or_waived"

if [[ -n "$missing" ]]; then
  echo "audit-integration-enrolment: FAIL — package(s) carry a '//go:build integration'" >&2
  echo "tag but are NOT enrolled in the integration job's package list in" >&2
  echo ".github/workflows/ci.yml (and are NOT on the slice-345 known-gaps allowlist):" >&2
  echo "" >&2
  while IFS= read -r p; do
    [[ -z "$p" ]] && continue
    echo "    - ./$p/..." >&2
  done <<<"$missing"
  echo "" >&2
  echo "Fix: add each package to the 'Run integration tests' go test invocation" >&2
  echo "in .github/workflows/ci.yml. Ship an integration_test.go, also enrol it." >&2
  echo "See CONTRIBUTING.md 'Integration-test enrolment' and slice 345." >&2
  exit 1
fi

tagged_n="$(wc -l < "$tagged_tmp" | tr -d ' ')"
listed_n="$(wc -l < "$listed_tmp" | tr -d ' ')"
waived_n="$(wc -l < "$waived_tmp" | tr -d ' ')"
echo "audit-integration-enrolment: OK — ${tagged_n} tagged package(s); ${listed_n} enrolled; ${waived_n} on the slice-345 known-gaps allowlist (drain: slice 390)."
exit 0
