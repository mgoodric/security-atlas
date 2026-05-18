// Slice 130 — unit coverage for the slice-125 layout's route-guard predicate.
//
// `canReachAuditLog` is exported as pure logic so the gate can be exercised
// without a fetch round-trip. The layout itself is a server component;
// vitest doesn't model server-component rendering, so we cover the predicate
// directly. The fetch + redirect surface is covered by the Playwright e2e
// spec (web/e2e/audit-log.spec.ts AC-8b + AC-8e).
//
// Matrix (P0-A3 fail-closed posture is the load-bearing case — assert it
// twice, once for "no roles field" and once for "roles is not an array"):
//
//   * admin true                                          -> ADMIT
//   * admin false, roles ["auditor"]                       -> ADMIT
//   * admin false, roles ["grc_engineer"]                  -> ADMIT
//   * admin false, roles ["admin"]                         -> ADMIT (canonical role grant)
//   * admin false, roles ["auditor", "viewer"]             -> ADMIT (one matching is enough)
//   * admin false, roles []                                -> REDIRECT
//   * admin false, roles ["viewer"]                        -> REDIRECT (unrelated role)
//   * admin false, roles ["control_owner"]                 -> REDIRECT (canonical, but not in the trio)
//   * admin false, roles missing                           -> REDIRECT (P0-A3 fail-closed)
//   * admin false, roles=null                              -> REDIRECT (P0-A3 fail-closed)
//   * admin false, roles="auditor" (string, not array)     -> REDIRECT (P0-A3 fail-closed)
//   * empty body                                            -> REDIRECT
//   * roles contains non-string entries (filtered, none match)-> REDIRECT

import { describe, expect, test } from "vitest";

import { canReachAuditLog } from "./layout";

describe("canReachAuditLog", () => {
  test("admit: is_admin true overrides roles entirely", () => {
    expect(canReachAuditLog({ is_admin: true })).toBe(true);
    expect(canReachAuditLog({ is_admin: true, roles: [] })).toBe(true);
    expect(canReachAuditLog({ is_admin: true, roles: ["viewer"] })).toBe(true);
  });

  test("admit: roles includes 'auditor'", () => {
    expect(canReachAuditLog({ is_admin: false, roles: ["auditor"] })).toBe(
      true,
    );
  });

  test("admit: roles includes 'grc_engineer'", () => {
    expect(canReachAuditLog({ is_admin: false, roles: ["grc_engineer"] })).toBe(
      true,
    );
  });

  test("admit: roles includes 'admin' (explicit role grant, not just is_admin)", () => {
    expect(canReachAuditLog({ is_admin: false, roles: ["admin"] })).toBe(true);
  });

  test("admit: one matching role is enough", () => {
    expect(
      canReachAuditLog({
        is_admin: false,
        roles: ["auditor", "viewer", "control_owner"],
      }),
    ).toBe(true);
  });

  test("redirect: empty roles array", () => {
    expect(canReachAuditLog({ is_admin: false, roles: [] })).toBe(false);
  });

  test("redirect: only unrelated role 'viewer'", () => {
    expect(canReachAuditLog({ is_admin: false, roles: ["viewer"] })).toBe(
      false,
    );
  });

  test("redirect: canonical role 'control_owner' is NOT in the audit-log trio", () => {
    expect(
      canReachAuditLog({ is_admin: false, roles: ["control_owner"] }),
    ).toBe(false);
  });

  test("redirect: roles field missing (P0-A3 fail-closed)", () => {
    expect(canReachAuditLog({ is_admin: false })).toBe(false);
  });

  test("redirect: roles is null (P0-A3 fail-closed)", () => {
    expect(canReachAuditLog({ is_admin: false, roles: null })).toBe(false);
  });

  test("redirect: roles is a string, not an array (P0-A3 fail-closed)", () => {
    expect(canReachAuditLog({ is_admin: false, roles: "auditor" })).toBe(false);
  });

  test("redirect: empty body (everything undefined)", () => {
    expect(canReachAuditLog({})).toBe(false);
  });

  test("redirect: roles contains non-string entries, no matching string", () => {
    expect(
      canReachAuditLog({
        is_admin: false,
        roles: [42, null, { auditor: true }, "viewer"],
      }),
    ).toBe(false);
  });

  test("admit: roles contains non-string entries, one matching string", () => {
    expect(
      canReachAuditLog({
        is_admin: false,
        roles: [42, null, "auditor"],
      }),
    ).toBe(true);
  });
});
