- **frontend:** slice 253 — control-detail view
  (`/controls/{id}`) no longer ships five "endpoint not on main yet"
  empty-states for endpoints that have shipped. The surfaces were
  flagged by slice 204's per-page audit (Finding 1, Category
  (iv) mockup-stale, severity high). Re-points: the KPI
  `Evidence records · 30d` and the center-column Evidence stream
  card bind to `GET /v1/evidence?control_id=…` (slice 106); the
  right-rail Policies, Risks-treated, and Audit-log cards bind to
  `GET /v1/controls/{id}/{policies,risks,history}` (slice 064).
  Three new BFF route handlers under
  `web/app/api/controls/[id]/{policies,risks,history}/route.ts` mirror
  the existing `coverage`/`state`/`effectiveness` per-control proxy
  shape — vitest covers the 401/200/404/5xx response classes for each.
  When an endpoint returns `{count: 0}` the cards render an honest
  truly-empty state ("No policies are linked to this control",
  "No evaluation history yet for this control", etc.) — never the
  pre-slice-253 endpoint-pending copy and never a fabricated row
  (P0-253-2). The slice 041 docblock and the
  `web/e2e/control-detail.spec.ts` AC-4 expectation are updated to
  reflect the post-slice-106 / per-control-history-policies-risks
  reality. No upstream `internal/api/` handler modifications
  (P0-253-1). Closes spillover #253 from the slice 204 audit fleet;
  upholds the canvas §1.6 UI-honesty invariant in its denial-direction
  (the dual of "promise what we don't have"). Detailed audit trail in
  `docs/audit-log/204-page-audit-control.md` Finding 1.
