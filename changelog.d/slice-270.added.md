- **backend, frontend:** slice 270 — non-admin `/activity` ledger
  surface. Unblocks slice 232's dashboard activity-feed "View full
  activity ledger" footer link (which had no destination until now)
  and gives every signed-in tenant member a paginated view of
  tenant-public events plus their own self-audit trail. Two changes:
  a new HTTP route `GET /v1/activity/unified` mounted in
  `internal/api/adminauditlog/activity.go` that shares the slice 124
  unified-aggregator (`internal/audit/unifiedlog.Query`) via two new
  SQL parameters (`caller_is_privileged BOOLEAN`,
  `caller_user_id TEXT`) gating one extra row-visibility WHERE
  predicate; and a new Next.js route `/activity` with BFF at
  `web/app/api/activity/route.ts` (resolves the `?actor=me` sentinel
  to the caller's user_id server-side, slice 270 D5). The OPA
  admit-set lives on the existing slice 156 `"activity"` resource
  type — admin / auditor / grc_engineer / viewer / control_owner all
  admit; non-privileged callers see tenant-public kinds (decision,
  evidence, exception, sample, audit_period, aggregation_rule,
  walkthrough) plus me-rows where `actor_id = caller`. Admin-only
  rows (feature_flag flips, cross-actor me-rows including
  super_admin grants and tenant_create events) are hidden at the SQL
  layer regardless of URL filter (P0-A5 — filter-combination authz
  independence). No new schema migration (P0-A3 — reuses slice 124's
  substrate). The slice 124 admin endpoint
  (`/v1/admin/audit-log/unified`) is unchanged in admit-set and
  behavior; both endpoints share the same SQL, with the admin route
  passing `caller_is_privileged=true` to short-circuit the new
  predicate. Meta-audit row reuses `audit_log_query_unified` action
  (no `me_audit_log.action` CHECK extension); the new `surface`
  field on the audit blob (`"admin"` vs `"activity"`) splits the two
  endpoints for forensic review. Sidebar entry added unconditionally
  (slice 270 D4 — no role gate, mirrors the dashboard /
  calendar / metrics cross-business cluster). The slice-125
  `<UnifiedAuditTable>` page-client is duplicated into the new
  `web/app/(authed)/activity/page-client.tsx` (slice 270 D3 —
  bounded duplication; a follow-on slice can extract a shared
  component). Cross-actor + cross-tenant + filter-independence
  isolation pinned by four integration tests in
  `internal/api/adminauditlog/activity_integration_test.go`. OPA
  admit matrix pinned by `internal/authz/slice270_test.go`
  (five-role admit + write-deny). All JUDGMENT calls captured in
  `docs/audit-log/270-non-admin-activity-ledger-decisions.md`.
  Closes
  [#270](https://github.com/mgoodric/security-atlas/issues/270);
  unblocks
  [#232](https://github.com/mgoodric/security-atlas/issues/232).
