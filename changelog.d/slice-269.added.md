- **api:** slice 269 — dashboard snapshot export endpoint
  (`GET /v1/dashboard/export?format=json|csv|xlsx`). Composes the
  six dashboard panels (framework posture, risks, freshness, drift,
  upcoming, activity) into a single artifact in three formats:
  single JSON document, ZIP of one CSV per panel, or XLSX workbook
  with one sheet per panel. New package
  `internal/api/dashboardexport/` reuses the slice 066 dashboard
  store (with two new exported wrappers `ActivityFeedFirstPage` +
  `UpcomingItemsFirstPage` on `dashboard.Store` so cross-package
  callers can read the first page without touching the
  package-private `keyset` type), the slice 019 risk store, and
  the slice 016 freshness + drift stores; no new SQL queries.
  Migration `20260524000000_dashboard_export_meta_audit.sql`
  extends `me_audit_log.action` CHECK to permit the new
  `dashboard_export` value; idempotent + reversible. Role gate is
  admin + approver only (slice 269 D3 — narrower than slice 156's
  in-app dashboard reads which also admit `control_owner`); the
  slice 035 OPA admit pins the same shape in `auditor.rego` (admit
  set `"dashboard"`), tested in
  `internal/authz/slice269_test.go::TestSlice269_DashboardExportAdmitMatrix`.
  Every terminal outcome writes one `me_audit_log` row with
  `action='dashboard_export'` (success / denied:_ / error:_),
  mirroring the slice 137 / 138 / 175 forensic shape. Streaming
  encoders (multi-sheet XLSX, zip-of-CSVs, single-doc JSON) live
  in-package because the slice 135 `internal/export/` library
  ships single-table encoders only; the 50K-row streaming-memory
  test asserts live heap delta stays under 200 MB for each
  format. Cross-tenant isolation integration test
  (`TestSlice269_CrossTenantIsolation`) seeds a tenant-B probe
  title and asserts it does NOT appear in tenant A's export across
  all three formats — the merge-blocking evidence for
  constitutional invariant #6 (RLS / tenancy). Unblocks slice
  230's frontend `Export` CTA wiring. Decisions log:
  `docs/audit-log/269-dashboard-export-endpoint-decisions.md`.
