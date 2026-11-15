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
// Why only `super_admin`? Slice 186's AC-2 named three variants
// (`admin`, `tenant_admin`, `super_admin`), but the /admin layout gate
// admits on `is_admin`, which is super-admin only. Once /v1/me began
// returning real roles (#2216), listing the tenant variants would show
// tenant admins an entry that only leads to the denial page. Do NOT
// widen this list locally; a change to who /admin is for is a decision.
//
// Located in `web/lib/` rather than `web/components/shell/` so the
// existing vitest config (`web/vitest.config.ts` include pattern
// `lib/**/*.test.ts`) covers it without the JSX rendering harness
// that slice 069 P0-A3 forbids. Pattern follows slice 130's
// `canReachAuditLog` predicate exactly.

// ADMIN_NAV_ROLES — the canonical set of role strings that grant the
// sidebar's "Admin" entry. MUST match who the /admin layout gate admits.
// #2216 (Matt, 2026-11-14): /admin is super-admin only, so a tenant
// `admin` / `tenant_admin` grant does NOT show the entry; it would only
// lead to the denial page. See docs/audit-log/2216-admin-gate-decisions.md.
const ADMIN_NAV_ROLES = ["super_admin"] as const;
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
