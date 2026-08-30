#!/usr/bin/env bash
#
# delivery-open-adapter.sh — falsifier for isa/assessor-delivery.md ISC-8
# (D5 · Openness): "A self-hoster can deliver without a commercial
# relationship. Falsifier: every shipped adapter requiring vendor-issued
# credentials." Test Strategy row: "at least one shipped adapter completes
# with no vendor credential", threshold 1.
#
# The epic is phase: specified, progress: 0 (isa/assessor-delivery.md
# frontmatter) — no assessor-delivery code has landed. Canvas §8.6 and OQ #22
# (Plans/canvas/11-open-questions.md) commit the eventual shape: a
# `Deliverer` interface, `AssessorDestination` rows, and — per OQ #22 Shape
# B, the reading the epic's own Test Strategy is written against — exactly
# one shipped OPEN adapter (signed-OSCAL-bundle HTTP POST to any
# operator-controlled endpoint) that any self-hoster can exercise with zero
# vendor credential. None of that exists in the tree today.
#
# This probe does not attack a lower-level primitive the way delivery-ssrf.sh
# does, because there is no existing egress-adapter primitive for assessor
# delivery to inherit from: `internal/notify`'s `Deliverer` (grep-confirmed,
# internal/notify/scheduler/channels.go) is an unrelated digest email/Slack/
# webhook-notification concept, not an assessor-delivery seam. So this probe
# checks the fact ISC-8's threshold is actually about directly: how many
# shipped assessor-delivery adapters exist that complete with no vendor
# credential. It answers that by searching every place shipped code would
# have to touch for an adapter to be real — Go source (internal/,
# connectors/, cmd/, pkg/, proto/) and the schema (migrations/sql/) the
# epic's own D1 claims (ISC-1, ISC-2) require every delivery to be backed by
# (`assessor_destinations`, `assessor_deliveries`) — for any reference to
# assessor delivery at all.
#
# Zero hits across all of it is a positive-confirmation fact about the tree,
# not an absence read off one failed command: the shipped-adapter count is
# 0, which is below ISC-8's threshold of 1, so the claim does not hold today.
# Nothing here needs a database, NATS, MinIO or a running service, so this
# is CANNOT RUN only when the search itself cannot be performed (grep
# missing, or this is not a security-atlas checkout).
#
# A future implementer makes ISC-8 pass by shipping at least one adapter
# whose delivery path completes with no `credential_ref` / vendor API key
# configured on its `assessor_destinations` row — the open, signed-bundle
# HTTP POST adapter OQ #22 Shape B names. Once ANY assessor-delivery code
# lands, a source-grep for "assessor" stops being the right instrument
# (finding a file does not by itself prove an adapter is open) — this probe
# reports CANNOT RUN in that case rather than guessing at an unshipped
# adapter registry's shape, and the claim needs a behavioral successor probe
# at that point (e.g. drive the CLI/API with no credential configured and
# confirm a delivery completes).
#
# Exit 0 — at least one shipped, credential-free adapter was found. Not
#          reachable today; nothing has shipped.
# Exit 1 — zero shipped assessor-delivery adapters exist anywhere in the
#          tree: the ISC-8 threshold (>=1) is unmet, so the claim is false.
# Exit 3 — a precondition was missing (find/grep unavailable, repo root not
#          found), OR partial assessor-delivery code was found but this
#          probe has no mechanical way to determine adapter credential
#          requirements from it.

set -euo pipefail

if ! command -v grep >/dev/null 2>&1; then
  echo "CANNOT RUN: 'grep' not on PATH" >&2
  exit 3
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

if [[ ! -d internal || ! -d migrations/sql ]]; then
  echo "CANNOT RUN: expected internal/ and migrations/sql/ under $REPO_ROOT — not in a security-atlas checkout" >&2
  exit 3
fi

# Every place shipped adapter code, its registration, or its schema would
# have to appear. isa/, Plans/ and docs/ are deliberately excluded: a claim
# or a design doc mentioning "assessor" is not shipped code, and including
# them would make this probe pass on its own falsifier's prose.
SEARCH_DIRS=(internal connectors cmd pkg proto migrations/sql)
EXISTING_DIRS=()
for d in "${SEARCH_DIRS[@]}"; do
  [[ -d "$d" ]] && EXISTING_DIRS+=("$d")
done

# A bare substring match on "assessor" is too loose: internal/authz/
# rego_bundle/auditor.rego already says "auditor is the external assessor
# role" in a comment, which has nothing to do with the delivery seam and
# would misfire this probe into CANNOT RUN. Anchor instead on the epic's own
# vocabulary — the Go types canvas §8.6 commits to (AssessorDestination,
# the Deliverer/adapter shape) and the table names ISC-1/ISC-2 name
# (assessor_destinations, assessor_deliveries) — so a false-positive prose
# mention can't be mistaken for shipped code.
PATTERN='Assessor(Destination|Deliver(y|ies|er)|Adapter)|assessor_(destinations|deliveries)'
HITS="$(grep -rIlE "$PATTERN" "${EXISTING_DIRS[@]}" 2>/dev/null || true)"

if [[ -z "$HITS" ]]; then
  echo "FALSE: grep -rIlE for $PATTERN across ${SEARCH_DIRS[*]} found nothing shipped — no Deliverer implementation, no assessor_destinations/assessor_deliveries migration, no CLI or SDK surface. Zero shipped adapters means the >=1-open-adapter threshold ISC-8's falsifier requires is unmet by construction: a self-hoster cannot deliver evidence to an assessor today, open adapter or otherwise."
  exit 1
fi

{
  echo "CANNOT RUN: found references to \"assessor\" in shipped code/schema, but this probe only checks for shipped-adapter ABSENCE (see header) and cannot mechanically determine which of the files below is a real adapter or whether it requires a vendor credential. Extend or replace this probe with a behavioral check (drive delivery with no credential configured and confirm it completes) now that assessor-delivery code exists:"
  echo "$HITS"
} >&2
exit 3
