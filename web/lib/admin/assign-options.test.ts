// Slice 527 — vitest for the assign-dialog option-mapping + pinning logic.
//
// This module is the pure, node-testable core of the slice-527 dropdowns
// (D2 in the decisions log): mapping the loaded user list / tenant list to
// <select> options and computing the tenant-field mode (chooser vs pinned)
// from the cross_tenant signal + the session tenant. JSX wiring is covered
// by the Playwright e2e (admin-users.spec.ts); per slice 069 vitest is
// node-only.

import { describe, expect, test } from "vitest";

import {
  userOptions,
  tenantOptions,
  tenantFieldMode,
  type AssignUserOption,
  type AssignTenantOption,
} from "./assign-options";
import type { AdminUserRow, TenantRow } from "@/lib/api/admin";

const TENANT_A = "11111111-1111-4111-8111-111111111111";
const TENANT_B = "22222222-2222-4222-8222-222222222222";
const USER_1 = "aaaaaaaa-1111-4111-8111-111111111111";
const USER_2 = "bbbbbbbb-2222-4222-8222-222222222222";

function row(over: Partial<AdminUserRow>): AdminUserRow {
  return {
    id: USER_1,
    email: "alpha@example.com",
    display_name: "Alpha User",
    status: "active",
    roles: ["admin"],
    ...over,
  };
}

function tenant(over: Partial<TenantRow>): TenantRow {
  return {
    id: TENANT_A,
    name: "Tenant A",
    is_bootstrap_tenant: false,
    created_at: "2026-05-22T00:00:00.000Z",
    ...over,
  };
}

describe("userOptions", () => {
  test("maps each user to {value:id, label: display-name + email}", () => {
    const opts = userOptions([
      row({
        id: USER_1,
        display_name: "Alpha User",
        email: "alpha@example.com",
      }),
      row({
        id: USER_2,
        display_name: "Bravo User",
        email: "bravo@example.com",
      }),
    ]);
    expect(opts).toEqual<AssignUserOption[]>([
      { value: USER_1, label: "Alpha User (alpha@example.com)" },
      { value: USER_2, label: "Bravo User (bravo@example.com)" },
    ]);
  });

  test("de-duplicates by user id (cross-tenant list has N rows per person)", () => {
    // Super-admin cross-tenant shape: USER_1 appears under two tenants.
    const opts = userOptions([
      row({ id: USER_1, tenant_id: TENANT_A }),
      row({ id: USER_1, tenant_id: TENANT_B }),
      row({
        id: USER_2,
        tenant_id: TENANT_A,
        display_name: "Bravo User",
        email: "bravo@example.com",
      }),
    ]);
    expect(opts.map((o) => o.value)).toEqual([USER_1, USER_2]);
  });

  test("falls back to email-only label when display_name is empty", () => {
    const opts = userOptions([
      row({ id: USER_1, display_name: "", email: "noname@example.com" }),
    ]);
    expect(opts[0].label).toBe("noname@example.com");
  });

  test("returns [] for an empty list", () => {
    expect(userOptions([])).toEqual([]);
  });
});

describe("tenantOptions", () => {
  test("maps each tenant to {value:id, label:name}", () => {
    const opts = tenantOptions([
      tenant({ id: TENANT_A, name: "Tenant A" }),
      tenant({ id: TENANT_B, name: "Tenant B" }),
    ]);
    expect(opts).toEqual<AssignTenantOption[]>([
      { value: TENANT_A, label: "Tenant A" },
      { value: TENANT_B, label: "Tenant B" },
    ]);
  });

  test("returns [] for an empty list", () => {
    expect(tenantOptions([])).toEqual([]);
  });
});

describe("tenantFieldMode", () => {
  test("super-admin (crossTenant) → chooser, regardless of session tenant", () => {
    expect(
      tenantFieldMode({ crossTenant: true, sessionTenantId: TENANT_A }),
    ).toEqual({
      kind: "chooser",
    });
  });

  test("tenant-admin (not crossTenant) with a session tenant → pinned to it", () => {
    expect(
      tenantFieldMode({ crossTenant: false, sessionTenantId: TENANT_A }),
    ).toEqual({ kind: "pinned", tenantId: TENANT_A });
  });

  test("tenant-admin without a resolved session tenant → unresolved (submit blocked)", () => {
    expect(
      tenantFieldMode({ crossTenant: false, sessionTenantId: undefined }),
    ).toEqual({ kind: "unresolved" });
    expect(
      tenantFieldMode({ crossTenant: false, sessionTenantId: "" }),
    ).toEqual({ kind: "unresolved" });
  });
});
