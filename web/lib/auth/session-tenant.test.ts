// #2399 — the session's tenant, for keying browser caches. Anything that
// is not a well-formed token with a UUID tenant claim is unknown.

import { describe, expect, it } from "vitest";

import { tenantIdFromJwt } from "./session-tenant";

const TENANT = "aaaaaaaa-0000-4000-8000-000000000001";

function jwt(claims: unknown): string {
  const enc = (v: unknown) =>
    Buffer.from(JSON.stringify(v)).toString("base64url");
  return `${enc({ alg: "ES256" })}.${enc(claims)}.sig`;
}

describe("tenantIdFromJwt", () => {
  it("reads atlas:current_tenant_id", () => {
    expect(tenantIdFromJwt(jwt({ "atlas:current_tenant_id": TENANT }))).toBe(
      TENANT,
    );
  });

  it("normalises the case", () => {
    expect(
      tenantIdFromJwt(jwt({ "atlas:current_tenant_id": TENANT.toUpperCase() })),
    ).toBe(TENANT);
  });

  it.each([
    ["no token", undefined],
    ["empty", ""],
    ["not a JWT", "opaque-api-key"],
    ["bad payload", "a.!!!.c"],
    ["payload not an object", jwt("x")],
    ["no claim", jwt({ sub: "u" })],
    ["claim not a UUID", jwt({ "atlas:current_tenant_id": "tenant-a" })],
    ["claim not a string", jwt({ "atlas:current_tenant_id": 7 })],
  ])("is unknown for %s", (_, token) => {
    expect(tenantIdFromJwt(token)).toBeUndefined();
  });
});
