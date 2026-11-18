// Who may export the unified audit log (CSV / JSON / XLSX).
//
// Moved from the retired /audit-log layout guard (#2093). The Activity
// page reads every row through `/v1/activity/unified`, which any tenant
// member reaches; the export endpoint `/v1/admin/audit-log/export` is
// narrower, so the page shows the export bar only to callers this
// predicate admits. The backend stays the authority: a caller who
// reaches the export URL without the role still gets a 403.
//
// AUDIT_LOG_ROLES MUST match `internal/db/queries/unified_audit_log_role.sql`
// (slice 124) — treat it as one literal in two languages. Admins reach it
// through `is_admin` (super-admin) or `is_tenant_admin` (#2503 D5, Matt
// 2026-11-18: an org admin reads and exports its own organisation's log;
// the handler admits authz.IsTenantAdmin). A bare `admin` string in `roles`
// still does not count: it is not the server's live-role answer.
//
// Fail-closed (slice 130 P0-A3): a missing or non-array `roles` field
// collapses to `[]`, and the predicate then admits only on
// `is_admin === true` or `is_tenant_admin === true`.

const AUDIT_LOG_ROLES = ["auditor", "grc_engineer"] as const;
type AuditLogRole = (typeof AUDIT_LOG_ROLES)[number];

export function canReachAuditLog(body: {
  is_admin?: boolean;
  is_tenant_admin?: boolean;
  roles?: unknown;
}): boolean {
  if (body.is_admin === true || body.is_tenant_admin === true) {
    return true;
  }
  if (!Array.isArray(body.roles)) {
    return false;
  }
  return body.roles.some(
    (r): r is AuditLogRole =>
      typeof r === "string" &&
      (AUDIT_LOG_ROLES as readonly string[]).includes(r),
  );
}
