import { describe, expect, it } from "vitest";

import type { AdminUserRow, SuperAdminRow } from "@/lib/api/admin";

import {
  LAST_ADMIN_REASON,
  LAST_SUPER_ADMIN_REASON,
  lastAdminReason,
  lastSuperAdminReason,
} from "./last-holder";

function user(
  id: string,
  roles: string[],
  extra: Partial<AdminUserRow> = {},
): AdminUserRow {
  return {
    id,
    email: `${id}@x`,
    display_name: id,
    status: "active",
    roles,
    ...extra,
  };
}

describe("lastAdminReason", () => {
  it("withholds revoke from a tenant's only active admin", () => {
    const rows = [user("a", ["admin"]), user("b", ["viewer"])];
    expect(lastAdminReason(rows[0], rows, true)).toBe(LAST_ADMIN_REASON);
    expect(lastAdminReason(rows[1], rows, true)).toBeUndefined();
  });

  it("offers revoke when the tenant has a second active admin", () => {
    const rows = [user("a", ["admin"]), user("b", ["admin", "viewer"])];
    expect(lastAdminReason(rows[0], rows, true)).toBeUndefined();
  });

  it("does not count a disabled admin", () => {
    const rows = [
      user("a", ["admin"]),
      user("b", ["admin"], { status: "disabled" }),
    ];
    expect(lastAdminReason(rows[0], rows, true)).toBe(LAST_ADMIN_REASON);
  });

  it("counts admins per tenant in the cross-tenant shape", () => {
    const rows = [
      user("a", ["admin"], { tenant_id: "t1" }),
      user("b", ["admin"], { tenant_id: "t2" }),
      user("c", ["admin"], { tenant_id: "t2" }),
    ];
    expect(lastAdminReason(rows[0], rows, true)).toBe(LAST_ADMIN_REASON);
    expect(lastAdminReason(rows[1], rows, true)).toBeUndefined();
  });

  it("withholds nothing from a partial list", () => {
    const rows = [user("a", ["admin"])];
    expect(lastAdminReason(rows[0], rows, false)).toBeUndefined();
  });
});

describe("lastSuperAdminReason", () => {
  const sa = (id: string): SuperAdminRow => ({
    user_id: id,
    granted_at: "2026-01-01T00:00:00Z",
    granted_via: "manual_grant",
  });

  it("withholds demote when one super admin remains", () => {
    expect(lastSuperAdminReason([sa("a")])).toBe(LAST_SUPER_ADMIN_REASON);
  });

  it("offers demote when there are two", () => {
    expect(lastSuperAdminReason([sa("a"), sa("b")])).toBeUndefined();
  });
});
