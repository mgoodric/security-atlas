// #2385 — the name source for /admin/super-admins. Super admins span
// tenants and `/v1/people` lists the session tenant only (by design), so
// <Person> on that page resolves against the super-admin list itself:
// GET /v1/admin/super-admins names each row from the user's own tenant.

import type { SuperAdminRow } from "@/lib/api/admin";
import { buildDirectory, type PersonDirectory } from "@/lib/person";

/**
 * A directory of the listed super admins. A row's name is its display
 * name, else its email's local part (the `/v1/people` fallback); a row
 * with neither resolves to "Unknown".
 */
export function superAdminDirectory(
  rows: readonly SuperAdminRow[],
): PersonDirectory {
  return buildDirectory(
    rows.map((s) => ({
      id: s.user_id,
      display_name: s.display_name?.trim() || s.email?.split("@")[0] || "",
      status: "active",
    })),
    [],
  );
}
