# 175 — Control bundle history export (lineage including superseded versions)

**Cluster:** Backend / Frontend
**Estimate:** 1d
**Type:** AFK (no JUDGMENT — column shape is dictated by superseded-row schema)
**Status:** `not-ready`

## Narrative

Spillover from slice 137 — slice 137 D2 explicitly excluded
`superseded_by` / `superseded_at` columns from the controls export
because the slice 137 query filters to active rows
(`superseded_by IS NULL`), making those columns always-NULL noise.

For an auditor reconstructing "what did this control look like at
audit-period freeze T?", the SUPERSEDED versions matter. This slice
ships a separate `GET /v1/controls/history/export` that exports the
full bundle lineage — every version, active + superseded — with the
supersession chain columns surfaced.

**What this slice ships:** `GET /v1/controls/history/export?format=...`
that drops the `superseded_by IS NULL` filter and includes:

- All columns from slice 137's export
- Plus `superseded_by` (UUID of the row that superseded this one;
  null for active rows)
- Plus `superseded_at` (timestamp of supersession; null for active)
- Plus `version` ordering (descending per bundle so consumers see
  the most-recent-first lineage)

**Scope discipline (what is OUT):** sample populations frozen at
audit-period boundaries (slice 028 territory); evidence record
lineage (separate concern); cosigned bundle artifacts (slice 030).

## Threat model

Inherits slice 135 + slice 137. The additional disclosure surface
is the supersession metadata — who superseded what, when. Operators
who can read active controls can read history (same RLS posture).

## Acceptance criteria (stub — expand at pickup)

- [ ] AC-1: `GET /v1/controls/history/export?format=...` reuses
      slice 135 library with the active-only filter removed.
- [ ] AC-2: 17-column set (slice 137's 15 + `superseded_by` + `superseded_at`).
- [ ] AC-3: BFF route + Export button (or dropdown option) on the
      controls page.
- [ ] AC-4: Cross-tenant isolation integration test.
- [ ] AC-5: Meta-audit row (action = `controls_history_export`).
- [ ] AC-6: Streaming-memory test asserts under 200 MB.
- [ ] AC-7: CHANGELOG entry.

## Constitutional invariants honored

Inherits slice 135 + slice 137. The history export preserves the
constitutional ledger-shape of the controls table: append-only with
supersession markers; no row is ever deleted.

## Dependencies

- **#135** Data-export library. **Gate: 135 merged.** (Already merged.)
- **Audit-period attestation workflow** (slice 028 family) shipping
  a sample-pull surface — this slice is provisional until there's a
  real operator workflow that needs the lineage view. The slice 028
  attestation ledger is the natural consumer.

## Anti-criteria (P0 — block merge)

- Inherits slice 135 + slice 137 P0s.
- **P0-A-175-1:** The 17-column set MUST include `superseded_by` and
  `superseded_at` as the two new columns; the prior 15 columns from
  slice 137 stay in the same positions for downstream-tool reuse.

## Skill mix

- slice 135's `internal/export/` library — consume only.
- Slight extension of slice 137's `ListActiveControlsForExport`
  query (drop the `superseded_by IS NULL` filter; sort by bundle_id
  ASC, version DESC).
- Go integration tests + Playwright e2e.

## Notes for the implementing agent

The handler shape is essentially slice 137 + 2 extra columns + a
relaxed WHERE clause. Mechanical port; no D1 expected. Spillover
provenance: filed 2026-05-19 from slice 137 D2 rejected
alternatives.
