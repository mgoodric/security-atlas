// Slice 186 — sidebar "Admin" entry visibility predicate.
//
// Closes F-178-6 (slice 178 first-pass audit): the sidebar previously
// rendered the `/admin` entry to every signed-in user. Non-admin
// callers clicked it, bounced off the slice-060 admin-layout authz
// gate, and lost confidence in the affordance honesty of the chrome.
//
// SCOPE — this predicate governs UI CHROME ONLY. The server-side authz
// gate at `web/app/admin/layout.tsx` is the security boundary and is
// unchanged. Hiding the sidebar entry is not an access decision; it is
// an affordance-honesty decision. A client-side role check is NOT a
// security boundary (P0-186-1).
//
// CONTRACT — `shouldShowAdminEntry` accepts the same wire shape the
// slice-130 BFF `/api/admin/me` route returns:
//
//   {
//     is_admin?: boolean,
//     roles?:    string[]   // platform always emits array; legacy
//                           // upstreams may emit undefined / non-array
//   }
//
// Admit (return true) when EITHER:
//   * `is_admin === true`                                  — slice-060/130 cred flag
//   * `roles` contains any of the canonical admin variants — `admin`,
//                                                            `super_admin`,
//                                                            `tenant_admin`
//
// Reject (return false) — including the P0-186-4 fail-closed cases:
//   * empty body
//   * `is_admin` missing or false AND `roles` missing
//   * `roles` is not an array (string, null, number, object, etc.)
//   * `roles` is an empty array
//   * `roles` contains only non-admin strings (viewer, control_owner,
//     grc_engineer, auditor, etc.)
//
// Why the three admin variants? The slice-035 OPA role grant table +
// slice-051 + slice-164 stack defines three role grants that map to
// the platform's admin scope. `admin` is the canonical role in the
// current single-tenant deployment; `tenant_admin` is the v2 SaaS
// shape; `super_admin` is the platform-operator role. The spec's AC-2
// names all three explicitly. Adding a fourth variant requires a new
// slice — do NOT widen this list locally.
//
// Located in `web/lib/` rather than `web/components/shell/` so the
// existing vitest config (`web/vitest.config.ts` include pattern
// `lib/**/*.test.ts`) covers it without the JSX rendering harness
// that slice 069 P0-A3 forbids. Pattern follows slice 130's
// `canReachAuditLog` predicate exactly.

// ADMIN_NAV_ROLES — the canonical set of role strings that grant the
// sidebar's "Admin" entry. MUST match the platform's user_roles table
// values for any role that maps to admin scope. A divergence between
// this constant and the platform's admin-role set is a silent UI gap
// (chrome hides an entry the user actually has access to).
const ADMIN_NAV_ROLES = ["admin", "super_admin", "tenant_admin"] as const;
type AdminNavRole = (typeof ADMIN_NAV_ROLES)[number];

/**
 * shouldShowAdminEntry returns true when the sidebar should render the
 * `/admin` nav entry for the supplied `/api/admin/me` body. P0-186-4
 * fail-closed: a missing / malformed body returns false (hide the
 * entry). Pure logic — no I/O, no React. Exported for direct vitest
 * coverage.
 */
export function shouldShowAdminEntry(body: {
  is_admin?: unknown;
  roles?: unknown;
}): boolean {
  if (body.is_admin === true) {
    return true;
  }
  if (!Array.isArray(body.roles)) {
    return false;
  }
  return body.roles.some(
    (r): r is AdminNavRole =>
      typeof r === "string" &&
      (ADMIN_NAV_ROLES as readonly string[]).includes(r),
  );
}
