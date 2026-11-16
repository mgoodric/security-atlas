- **controls:** slice 175 — control bundle history export (lineage
  including superseded versions). Adds
  `GET /v1/controls/history/export?format=<csv|json|xlsx>` —
  a sibling to slice 137's active-only `GET /v1/controls/export`. The
  history export returns 17 columns (slice 137's 15 columns IN THE
  SAME ORDER, followed by two new columns `superseded_by` +
  `superseded_at`) covering every version of every bundle, active +
  superseded. Ordered `bundle_id ASC, version DESC` so consumers see
  most-recent-first lineage per bundle. Forensic consumer: auditor
  period-freeze reconstruction ("what did this control look like at
  frozen_at T?"). New BFF route at `/api/controls/history/export`;
  new Export-History (CSV / JSON / XLSX) link group on `/controls`
  alongside the slice 137 buttons. Reuses slice 135's data-export
  library + slice 145's per-(tenant, user) concurrency cap. Meta-audit
  action `controls_history_export`. 200 MB streaming-memory budget at
  50K synthetic rows (AC-6).
