# 269 — Dashboard snapshot export endpoint (JSON / CSV / XLSX)

**Cluster:** Backend (export)
**Estimate:** ~1d
**Type:** AFK
**Status:** `ready`
**Parent:** spillover from slice 204 / precursor to slice 230 (dashboard Export + New-board-report header CTAs). Filed 2026-05-23 to unblock the "Export" half of slice 230 (the "New board report" half is already covered by slice 053 board-pack composer).

## Narrative

The slice 204 audit found that the dashboard mockup carries two header CTAs — "Export" + "New board report" — but neither is wired. The latter is backed by slice 053 (board-pack composer); the "Export" CTA has no backing endpoint. Slice 230 explicitly defers the endpoint shape choice to a separate slice; this slice owns that shape.

The endpoint exports a point-in-time snapshot of the dashboard's six panels (framework posture, risks summary, evidence freshness, drift, upcoming work, activity feed) in three formats: JSON (default), CSV (one file per panel, zipped), and XLSX (one sheet per panel). Reuses the slice 135 export library for format generation + the slice 138 export pattern for HTTP shape (`GET /v1/dashboard/export?format=json|csv|xlsx` → streaming download).

The "snapshot" definition: the same view the dashboard renders at request time. NOT a historical snapshot (that's slice 071's audit-period freezing surface — different concern). The export is operator-driven (admin only) — useful for emailing a board, archiving for compliance evidence, or sharing a status read with a non-atlas user.

### What ships in this slice

**Backend (`internal/api/dashboardexport/`):**

- New package `internal/api/dashboardexport/` with handler at `GET /v1/dashboard/export`.
- Query param `format=json|csv|xlsx` (default `json`).
- Reuses the existing per-panel data sources: `internal/api/dashboard/*` endpoints (slice 066 + 124 + others). No new queries; the export composes existing reads.
- Streaming output (XLSX uses the slice 135 streaming-zip pattern; CSV uses chunked write; JSON uses a single buffered write).
- OPA admit: `dashboard.export` action — same role gate as the existing dashboard read.
- meta_audit_log entry per export (slice 030 pattern); `action='dashboard_export'`.

**Migration**: extend `meta_audit_log.action` CHECK constraint to include `'dashboard_export'`. Pattern matches slice 175 (`controls_history_export`), slice 174 (`anchors_export`).

**No frontend in this slice.** Slice 230 wires the dashboard "Export" CTA to this endpoint after merge.

## Threat model

| STRIDE                       | Threat                                                                  | Mitigation                                                                                                                                                                                                          |
| ---------------------------- | ----------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | n/a — JWT auth + admin-role OPA gate.                                   | Inherits slice-190 jwtmw + slice-035 OPA + slice-211 admin grant.                                                                                                                                                   |
| **T** Tampering              | n/a — read-only export.                                                 | n/a                                                                                                                                                                                                                 |
| **R** Repudiation            | Admin exports the dashboard then denies doing so.                       | `meta_audit_log` entry per export captures actor_id, timestamp, format, ip; existing slice-030 pattern.                                                                                                             |
| **I** Information disclosure | Export contains data from other tenants if the underlying queries leak. | Each panel query is already RLS-gated by atlas_app + tenancy GUC. Cross-tenant isolation integration test verifies the composed export respects the boundary.                                                       |
| **D** DoS                    | Repeated XLSX export requests pile up streaming-zip buffers in memory.  | Streaming output bounded by the existing slice 135 memory cap (50K rows in <200MB). Add per-IP rate limit (1 export per 30s) via slice-188's token bucket; revisit if real usage shows higher cadence is warranted. |
| **E** EoP                    | Non-admin caller hits `/v1/dashboard/export` and gets sensitive data.   | OPA `dashboard.export` admit set restricted to admin + auditor; verified by an integration test that issues a JWT lacking those roles and asserts 403.                                                              |

## Acceptance criteria

- [ ] AC-1: `internal/api/dashboardexport/` package + chi mount at `GET /v1/dashboard/export`.
- [ ] AC-2: Query param `format` accepts `json|csv|xlsx`; default `json`; unknown value → 400.
- [ ] AC-3: JSON format returns `{snapshot_at, panels: {framework_posture, risks, freshness, drift, upcoming, activity}}` matching the dashboard's panel set.
- [ ] AC-4: CSV format returns a zip with one CSV per panel (filenames `framework-posture.csv`, etc.). Streaming.
- [ ] AC-5: XLSX format returns one workbook with one sheet per panel. Streaming via slice 135 library.
- [ ] AC-6: OPA admit set: `admin` + `auditor` only. Non-matching JWT returns 403 with the standard error shape.
- [ ] AC-7: `meta_audit_log` row written per request, `action='dashboard_export'`, with format + actor + timestamp.
- [ ] AC-8: Migration `20260524000000_dashboard_export_meta_audit.sql` extends `meta_audit_log.action` CHECK to include `'dashboard_export'`. Idempotent + reversible.
- [ ] AC-9: Cross-tenant isolation integration test: tenant A's export does NOT include tenant B's data.
- [ ] AC-10: 50K-row streaming-memory test asserts < 200MB resident memory for each format.
- [ ] AC-11: OpenAPI RouteSpec entry added.
- [ ] AC-12: CHANGELOG entry under `Added`. Slice 230's spillover called out as the unblocking target.

## Decisions

- **D1: Single endpoint, format query param** vs. three endpoints (`/v1/dashboard/export.csv` etc.) — chose query param for symmetry with slice 138's pattern.
- **D2: Reuse existing dashboard reads** rather than building dedicated "snapshot" queries. Keeps scope tight; the export is exactly what the live dashboard shows.
- **D3: Admin + auditor only** — narrow admit. A non-admin "export my own activity" surface is a separate, more nuanced slice; not in scope here.

## Constitutional invariants honored

- **RLS / tenancy (#6)**: reuses RLS-gated panel queries; cross-tenant isolation integration test asserts the boundary.
- **Audit-log integrity (#2)**: existing slice-030 meta_audit_log pattern + new CHECK value.
- **AI-assist boundary**: n/a — pure data export; no LLM in the loop.

## Anti-criteria (P0 — block merge)

- **P0-A1**: DOES NOT add new dashboard panels. Only exports the existing six.
- **P0-A2**: DOES NOT bypass per-panel RLS. Each panel query runs through the standard atlas_app + tenancy path.
- **P0-A3**: DOES NOT support historical / point-in-time snapshots. That's slice 071's audit-period surface; this is "right now" only.
- **P0-A4**: DOES NOT write to any production data table. The only write is the `meta_audit_log` entry.
- **P0-A5**: DOES NOT support formats beyond `json|csv|xlsx`. PDF is a future slice if demand surfaces.
- **P0-A6**: DOES NOT bypass the OPA admit on any path.

## Dependencies

- **#066** (dashboard backend endpoints) — merged. Per-panel reads.
- **#124** (unified audit-log aggregation) — merged. Activity-panel data source.
- **#135** (export library) — merged. Streaming format generators.
- **#138** (ledger entities export) — merged. HTTP shape reference.
- **#175** (controls history export) — merged. meta_audit_log CHECK-extension precedent.
- **#190** (jwtmw) — merged.
- **#035** (OPA middleware) — merged.

## Unblocks

- **#230** (dashboard Export + New-board-report header CTAs) — the "Export" half. The "New board report" half is already shipped via slice 053; after this slice merges, slice 230 can flip `not-ready` → `ready` (or be scope-reduced to just the FE wiring).

## Skill mix

- Go HTTP handler + chi mount
- Streaming response writers (XLSX zip, CSV chunk, JSON buffer)
- Migration (CHECK constraint extension)
- OPA admit-set extension
- Integration test against real Postgres

## Notes for the implementing agent

- The slice 135 streaming library lives at `internal/exportlib/` — reuse, don't reinvent.
- The slice 174/175/138 export-endpoint shapes are the closest templates. Pick one based on which best matches the multi-panel-per-export shape; slice 138 (ledger entities) is the strongest fit.
- The `meta_audit_log` CHECK migration is a one-liner: `ALTER TABLE meta_audit_log ... ADD VALUE 'dashboard_export'` or the equivalent CHECK update — match slice 175's pattern exactly.
- The cross-tenant isolation test should reuse the slice 030 / 138 two-tenant fixture pattern.
