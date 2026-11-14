// #1764 — tenant chrome follows the install's tenant count.

import { describe, expect, test } from "vitest";

import { parseTenancy, showTenantChrome } from "./tenancy";

describe("parseTenancy", () => {
  test("reads tenant_count + multi_tenant off the wire body", () => {
    expect(parseTenancy({ tenant_count: 1, multi_tenant: false })).toEqual({
      tenantCount: 1,
      multiTenant: false,
    });
    expect(parseTenancy({ tenant_count: 3, multi_tenant: true })).toEqual({
      tenantCount: 3,
      multiTenant: true,
    });
  });

  test("an unparseable body is unknown, not single-tenant", () => {
    expect(parseTenancy(null)).toBeUndefined();
    expect(parseTenancy({ tenant_count: "1" })).toBeUndefined();
    expect(parseTenancy({ multi_tenant: false })).toBeUndefined();
  });
});

describe("showTenantChrome", () => {
  test("hidden only when the install is known to have one tenant", () => {
    expect(showTenantChrome({ tenantCount: 1, multiTenant: false })).toBe(
      false,
    );
  });

  test("shown with two or more tenants", () => {
    expect(showTenantChrome({ tenantCount: 2, multiTenant: true })).toBe(true);
  });

  test("shown while unknown, so a failed probe looks like today", () => {
    expect(showTenantChrome(undefined)).toBe(true);
  });
});
