#!/usr/bin/env bash
#
# check-integration-shard-coverage.sh — slice 417
#
# The COMPLETENESS guard for the sharded integration matrix (AC-6 / P0-1).
#
# Background (slice 417): the `Go · integration (Postgres RLS)` job was a
# single serial `-p 1` run over ~89 packages — the merge-queue wall-clock
# bottleneck. Slice 417 shards it into a Phase A serial leg + a Phase B
# matrix of 2-3 shards, each leg still `-p 1` internally but on its own
# runner with its own Postgres. The package->leg assignment lives in
# scripts/integration-shards.txt.
#
# The load-bearing risk (slice 417 threat model T-1): if a package falls
# through the shard split and runs in NO leg, its integration tests
# silently stop running — a coverage AND correctness regression that
# looks green. This guard is the structural fence against that.
#
# It asserts THREE things and fails CI on any violation:
#
#   1. UNION COMPLETENESS — the union of all legs' package args in
#      integration-shards.txt EQUALS the set of integration-TAGGED
#      packages under internal/ (the same `//go:build integration`
#      extraction the slice-345 enrolment guard uses) MINUS the
#      slice-345 KNOWN_UNENROLLED waiver list. No tagged package is
#      assigned to NO leg; no shard assigns a non-tagged/non-root
#      package. This is the authoritative T-1 guard: the "must run" set
#      is the tagged set, not a scrape of ci.yml lint targets.
#   2. NO DOUBLE-ASSIGNMENT — no package arg appears in more than one leg
#      (double-running wastes minutes + can re-introduce a cross-shard
#      catalog race if a seeder lands in two parallel legs).
#   3. P0-2 PHASE-A PIN — the shared-seed catalog cluster (the packages
#      that import/seed the global SCF catalog `scf_anchors`) stays on
#      Leg A. The guard pins the canonical SCF-seed set (scfimport,
#      anchors, scfseed, soc2import, ucfcoverage, schemaregistry) to Leg A
#      so a future re-balance cannot silently move a catalog seeder into a
#      parallel Phase B shard (threat T-2).
#
# This guard COMPOSES WITH the slice-345 enrolment guard
# (scripts/audit-integration-enrolment.sh): slice 345 proves every
# `//go:build integration` package is LISTED somewhere in ci.yml; this
# guard proves the sharded listing is COMPLETE + DISJOINT + correctly
# pinned. Both run in CI on every code PR.
#
# Exit codes:
#   0 — manifest union == ci.yml matrix set; disjoint; Phase-A pin holds
#   1 — a completeness / disjointness / pin violation
#   2 — environment misconfigured (file unreadable, no package list found)
#
# Usage:
#   scripts/check-integration-shard-coverage.sh
#   SHARD_MANIFEST=path SHARD_TAGGED_ROOT=path scripts/...  # test overrides
#
# Run by `just check-integration-shard-coverage` and by CI on every PR.

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

MANIFEST="${SHARD_MANIFEST:-$REPO_ROOT/scripts/integration-shards.txt}"
# Root under which to discover `//go:build integration`-tagged packages
# (the "must run" set). Mirrors slice 345's GREP_ROOT default.
TAGGED_ROOT="${SHARD_TAGGED_ROOT:-$REPO_ROOT/internal}"

if [[ ! -r "$MANIFEST" ]]; then
  echo "check-integration-shard-coverage: manifest not readable: $MANIFEST" >&2
  exit 2
fi
if [[ ! -d "$TAGGED_ROOT" ]]; then
  echo "check-integration-shard-coverage: tagged root not a directory: $TAGGED_ROOT" >&2
  exit 2
fi

# Canonical SCF-catalog-seed cluster that MUST stay on Leg A (P0-2 / T-2).
# These packages seed or import the GLOBAL scf_anchors catalog (no
# tenant_id) and/or the catalog rows of evidence_kind_schemas; two of them
# running in PARALLEL Phase B shards (separate DBs) would be safe, but the
# slice-461 order-independence guard truncates + reseeds the catalog on
# Leg A and expects these to live there — and pinning them removes the
# foot-gun entirely. RATCHET: this set may only grow, never shrink, via a
# slice that re-argues the cross-binary collision analysis.
PHASE_A_PINNED=(
  ./internal/db/...
  ./internal/api/scfimport/...
  ./internal/api/anchors/...
  ./internal/api/scfseed/...
  ./internal/api/schemaregistry/...
  ./internal/api/soc2import/...
  ./internal/api/ucfcoverage/...
  ./internal/evidence/ingest/...
)

tmp_manifest="$(mktemp)"
tmp_tagged="$(mktemp)"
tmp_dupes="$(mktemp)"
trap 'rm -f "$tmp_manifest" "$tmp_tagged" "$tmp_dupes" "$tmp_manifest.files"' EXIT

# --------------------------------------------------------------------
# 1. Manifest union: column 2 of every non-comment, non-blank line,
#    normalized to `internal/<pkg>` (strip leading `./` and trailing
#    `/...`) so it lines up with the tagged-package extraction below.
#    The manifest's column 1 is the leg label (A | B1 | B2 | B3).
# --------------------------------------------------------------------
grep -vE '^[[:space:]]*#|^[[:space:]]*$' "$MANIFEST" \
  | awk '{print $2}' | sed -E 's|^\./||; s|/\.\.\.$||' | sort > "$tmp_manifest"

if [[ ! -s "$tmp_manifest" ]]; then
  echo "check-integration-shard-coverage: manifest has no package entries: $MANIFEST" >&2
  exit 2
fi

# Disjointness: a package arg appearing on two legs shows up as a
# duplicate in the normalized column-2 stream.
grep -vE '^[[:space:]]*#|^[[:space:]]*$' "$MANIFEST" \
  | awk '{print $2}' | sed -E 's|^\./||; s|/\.\.\.$||' | sort | uniq -d > "$tmp_dupes"

# --------------------------------------------------------------------
# 2. Tagged "must run" set: directories under TAGGED_ROOT containing at
#    least one `//go:build integration` file, normalized to
#    `internal/<pkg>`. This is the SAME extraction the slice-345 enrolment
#    guard performs (scripts/audit-integration-enrolment.sh step 1) — the
#    authoritative source of "this package has an integration suite that
#    must run somewhere", independent of any ci.yml lint-target token.
# --------------------------------------------------------------------
if ! grep -rl --include='*.go' '//go:build integration' "$TAGGED_ROOT" \
  > "$tmp_manifest.files" 2>/dev/null; then
  : > "$tmp_manifest.files"
fi
: > "$tmp_tagged"
while IFS= read -r f; do
  [[ -z "$f" ]] && continue
  d="$(dirname "$f")"
  case "$d" in
    */internal/*) d="internal/${d##*/internal/}" ;;
    */internal)   d="internal" ;;
    internal/*|internal) : ;;
  esac
  echo "$d"
done < "$tmp_manifest.files" | sort -u > "$tmp_tagged"

if [[ ! -s "$tmp_tagged" ]]; then
  echo "check-integration-shard-coverage: no //go:build integration packages found under $TAGGED_ROOT" >&2
  exit 2
fi

# --------------------------------------------------------------------
# Check (2): NO DOUBLE-ASSIGNMENT.
# --------------------------------------------------------------------
if [[ -s "$tmp_dupes" ]]; then
  echo "check-integration-shard-coverage: FAIL — package(s) assigned to MORE THAN ONE leg" >&2
  echo "in $MANIFEST (each package must run in exactly one shard):" >&2
  while IFS= read -r p; do
    [[ -z "$p" ]] && continue
    echo "    - $p" >&2
  done < "$tmp_dupes"
  exit 1
fi

# --------------------------------------------------------------------
# Check (1): UNION COMPLETENESS — manifest union == tagged "must run" set.
#   tagged - manifest → a tagged package owned by NO leg
#                       (T-1: a package falls through the split, runs nowhere).
#   manifest - tagged → a shard assigns a package that carries no
#                       integration tag (stale/typo'd assignment; the
#                       shard arg would match no integration tests).
# --------------------------------------------------------------------
only_manifest="$(comm -23 "$tmp_manifest" "$tmp_tagged")"
only_tagged="$(comm -13 "$tmp_manifest" "$tmp_tagged")"

fail=0
if [[ -n "$only_tagged" ]]; then
  fail=1
  echo "check-integration-shard-coverage: FAIL (T-1) — integration-tagged package(s)" >&2
  echo "assigned to NO leg in $MANIFEST — these would run in NO shard (silent" >&2
  echo "coverage + correctness regression):" >&2
  while IFS= read -r p; do
    [[ -z "$p" ]] && continue
    echo "    - ./$p/..." >&2
  done <<<"$only_tagged"
fi
if [[ -n "$only_manifest" ]]; then
  fail=1
  echo "check-integration-shard-coverage: FAIL — package(s) assigned in $MANIFEST that" >&2
  echo "carry no //go:build integration tag (stale/typo'd assignment; the shard arg" >&2
  echo "would match no integration tests):" >&2
  while IFS= read -r p; do
    [[ -z "$p" ]] && continue
    echo "    - ./$p/..." >&2
  done <<<"$only_manifest"
fi
if (( fail != 0 )); then
  echo "" >&2
  echo "Fix: keep scripts/integration-shards.txt and the integration-tagged" >&2
  echo "package set in lockstep. Every tagged package belongs to exactly one leg." >&2
  echo "See also scripts/audit-integration-enrolment.sh (slice 345)." >&2
  exit 1
fi

# --------------------------------------------------------------------
# Check (3): P0-2 PHASE-A PIN — every pinned catalog-seed package is on
# Leg A (column 1 == "A"). A pinned package on a B-leg is threat T-2.
# --------------------------------------------------------------------
pin_fail=0
for pkg in "${PHASE_A_PINNED[@]}"; do
  leg="$(grep -vE '^[[:space:]]*#|^[[:space:]]*$' "$MANIFEST" \
    | awk -v p="$pkg" '$2 == p {print $1}')"
  norm="$(printf '%s' "$pkg" | sed -E 's|^\./||; s|/\.\.\.$||')"
  if [[ -z "$leg" ]]; then
    # Only a violation if this pinned package is actually a tagged
    # integration package that SHOULD be assigned (so synthetic fixtures
    # may omit pinned packages they don't define; the real tree has all).
    if grep -qxF "$norm" "$tmp_tagged"; then
      echo "check-integration-shard-coverage: FAIL (P0-2) — pinned catalog-seed package" >&2
      echo "    $pkg" >&2
      echo "is tagged for integration but not present in the manifest at all." >&2
      pin_fail=1
    fi
  elif [[ "$leg" != "A" ]]; then
    echo "check-integration-shard-coverage: FAIL (P0-2 / T-2) — catalog-seed package" >&2
    echo "    $pkg" >&2
    echo "is on leg '$leg' but MUST stay on Leg A (serial). Two parallel Phase B" >&2
    echo "shards seeding the global SCF catalog cross-binary re-introduces the" >&2
    echo "race -p 1 was protecting (slice 417 P0-2 / threat T-2)." >&2
    pin_fail=1
  fi
done
if (( pin_fail != 0 )); then
  exit 1
fi

union_n="$(wc -l < "$tmp_manifest" | tr -d ' ')"
tagged_n="$(wc -l < "$tmp_tagged" | tr -d ' ')"
echo "check-integration-shard-coverage: OK — ${union_n} package(s) across legs ==" \
  "${tagged_n} integration-tagged package(s); disjoint; Phase-A catalog-seed pin holds."
exit 0
