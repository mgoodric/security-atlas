// #2090 — "last holder" guards for the admin row menus.
//
// Pure, so the node vitest tier covers them. Each returns the reason an
// action is unavailable, or undefined when it is offered. RowActions
// renders a reason as a disabled item with the reason as visible text.
//
// These are presentation only. They never replace a server refusal:
// the backend still answers 409 for the last super admin (slice 142
// P0-SA-1), and the page still surfaces that answer if a stale list
// lets the request through.

import type { AdminUserRow, SuperAdminRow } from "@/lib/api/admin";

export const LAST_ADMIN_REASON =
  "This is the tenant's only active admin. Assign admin to someone else first.";

export const LAST_SUPER_ADMIN_REASON =
  "This is the last super admin. Grant a successor first.";

function isActiveAdmin(u: AdminUserRow): boolean {
  return u.status === "active" && u.roles.includes("admin");
}

/**
 * The reason "Revoke roles" is withheld from `row`, or undefined.
 *
 * Withheld when `row` is the only active admin in its tenant. Rows are
 * grouped by `tenant_id` (the cross-tenant shape); the within-tenant
 * shape carries none, so every row is one tenant. `complete` is false
 * when the list is one page of several: other admins may be on a later
 * page, so nothing is withheld on a guess.
 */
export function lastAdminReason(
  row: AdminUserRow,
  rows: readonly AdminUserRow[],
  complete: boolean,
): string | undefined {
  if (!complete || !isActiveAdmin(row)) return undefined;
  const tenant = row.tenant_id ?? "";
  const admins = rows.filter(
    (u) => (u.tenant_id ?? "") === tenant && isActiveAdmin(u),
  );
  return admins.length === 1 ? LAST_ADMIN_REASON : undefined;
}

/** The reason "Demote" is withheld, or undefined: withheld when the
 *  list holds exactly one super admin. */
export function lastSuperAdminReason(
  rows: readonly SuperAdminRow[],
): string | undefined {
  return rows.length === 1 ? LAST_SUPER_ADMIN_REASON : undefined;
}
