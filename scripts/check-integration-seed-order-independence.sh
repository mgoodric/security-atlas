#!/usr/bin/env bash
#
# check-integration-seed-order-independence.sh — slice 461
#
# The order-independence GUARD for the SCF-catalog seed.
#
# Background (slice 461): the Go integration suite used to be order-COUPLED
# on shared SCF-catalog seed state. Several packages seeded the catalog
# lazily behind a brittle `if anchorCount == 0 { reseed }` guard that counted
# ALL rows in scf_anchors. A prior package that left the CURRENT SCF framework
# version PARTIAL (a "new release" import of 5 controls, a scoped DELETE, …)
# made that guard see "rows present" and skip the reseed, so the next
# package's SOC 2 crosswalk import failed with `scf_anchor "GOV-01" not
# found`. CI never hit it because its `tests-integration` package list is
# hand-ordered so the full SCF import runs FIRST. The fix made the seed
# completeness-aware (internal/api/scfseed) so it is order-INDEPENDENT.
#
# This guard is the regression fence: it RE-RUNS the catalog-sensitive
# integration packages in a DELIBERATELY non-curated (alphabetical) order
# against a freshly-truncated catalog and asserts green. If anyone
# reintroduces an order-coupled seed guard, this step fails even though the
# main (curated-order) run passed — which is exactly the silent-reorder
# fragility slice 461 closes. It runs INSIDE the existing integration job,
# which already has Postgres + the migrations applied, so it adds only a few
# seconds and no new infra.
#
# Requires (same env the integration job exports):
#   DATABASE_URL       — admin/migrate DSN (BYPASSRLS) for the truncate
#   DATABASE_URL_APP   — app-role DSN (the tests boot the server with this)
#
# Exit codes:
#   0 — the catalog-sensitive packages pass in non-curated order
#   1 — at least one package failed (order-coupling reintroduced)
#   2 — environment misconfigured (required env var unset, not in repo root)
#
# Local repro:
#   DATABASE_URL=... DATABASE_URL_APP=... \
#     bash scripts/check-integration-seed-order-independence.sh

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

if [[ -z "${DATABASE_URL:-}" ]]; then
  echo "check-integration-seed-order-independence: DATABASE_URL unset" >&2
  exit 2
fi
if [[ -z "${DATABASE_URL_APP:-}" ]]; then
  echo "check-integration-seed-order-independence: DATABASE_URL_APP unset" >&2
  exit 2
fi

# The catalog-sensitive packages, listed in ALPHABETICAL order — the
# canonical "anyone running ./internal/api/... locally" order, which is
# deliberately DIFFERENT from CI's curated `scfimport`-first ordering. If
# the seed is genuinely order-independent these pass regardless of order.
#
# This subset is intentionally small (the packages that seed or consume the
# shared SCF catalog), not the full suite: the guard's job is to prove
# order-independence of the SEED, not to re-run every integration package.
PACKAGES=(
  ./internal/api/anchors/...
  ./internal/api/scfimport/...
  ./internal/api/scfseed/...
  ./internal/api/soc2import/...
  ./internal/api/ucfcoverage/...
)

echo "check-integration-seed-order-independence: truncating shared catalog to a clean state…"
# Truncate the platform-layer catalog (and controls, whose RESTRICT FK on
# scf_anchors would otherwise block the seed packages' own wipes) so the
# non-curated run starts from the cold-start path — the path most likely
# to expose a reintroduced order-coupled guard. CASCADE handles the FK web.
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -q -c \
  "TRUNCATE controls, scf_anchors, fw_to_scf_edges, framework_requirements, framework_versions, frameworks CASCADE"

echo "check-integration-seed-order-independence: re-running catalog-sensitive packages in NON-CURATED (alphabetical) order…"
echo "  packages: ${PACKAGES[*]}"

# -count=1 disables the test cache so we actually re-execute against the
# freshly-truncated DB. -p 1 mirrors the main job's serialization.
if go test -tags=integration -p 1 -count=1 "${PACKAGES[@]}"; then
  echo "check-integration-seed-order-independence: OK — seed is order-independent."
  exit 0
fi

echo "check-integration-seed-order-independence: FAIL — a catalog-sensitive package" >&2
echo "failed when run in non-curated order. The SCF-catalog seed has become" >&2
echo "order-COUPLED again (slice 461 regression). Seed via internal/api/scfseed's" >&2
echo "EnsureFullCatalog / EnsureSCFCatalog (completeness-aware) rather than a" >&2
echo "row-count guard such as 'if anchorCount == 0 { reseed }'." >&2
exit 1
