import { describe, expect, test } from "vitest";

import { localLoginBody, loginTenantMode } from "./login-tenant";

const TENANT = "11111111-1111-4111-8111-111111111111";

describe("loginTenantMode (#1756)", () => {
  test("first install with a bootstrap tenant sends it hidden", () => {
    expect(
      loginTenantMode({
        first_install: true,
        tenant_id: TENANT,
        single_tenant: true,
      }),
    ).toEqual({ kind: "hidden", tenantID: TENANT });
  });

  test("after first install on a single tenant, no tenant is sent", () => {
    expect(
      loginTenantMode({ first_install: false, single_tenant: true }),
    ).toEqual({ kind: "implicit" });
  });

  test("several tenants ask for one, required", () => {
    expect(
      loginTenantMode({ first_install: false, single_tenant: false }),
    ).toEqual({ kind: "ask", required: true });
  });

  test("an older server without single_tenant asks, required", () => {
    expect(loginTenantMode({ first_install: false })).toEqual({
      kind: "ask",
      required: true,
    });
  });

  test("unreadable install-state still shows the form, tenant optional", () => {
    expect(loginTenantMode(null)).toEqual({ kind: "ask", required: false });
  });
});

describe("localLoginBody (#1756)", () => {
  test("omits tenant_id when empty", () => {
    expect(localLoginBody("", "a@example.com", "pw")).toEqual({
      email: "a@example.com",
      password: "pw",
    });
  });

  test("includes tenant_id when given", () => {
    expect(localLoginBody(TENANT, "a@example.com", "pw")).toEqual({
      tenant_id: TENANT,
      email: "a@example.com",
      password: "pw",
    });
  });
});
