// Slice 130 — unit coverage for the audit-log access predicate. It was the
// /audit-log layout's route guard; since #2093 it decides whether the
// Activity page shows the export bar (the export endpoint is narrower than
// the activity endpoint). The page wiring is covered by
// web/e2e/activity.spec.ts.
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

import { canReachAuditLog } from "./audit-log-access";

describe("canReachAuditLog", () => {
  // #2503 D5 (Matt, 2026-11-18): an org admin exports its own log.
  test("admit: is_tenant_admin true", () => {
    expect(canReachAuditLog({ is_admin: false, is_tenant_admin: true })).toBe(
      true,
    );
  });

  test("redirect: is_tenant_admin false with no matching role", () => {
    expect(
      canReachAuditLog({ is_admin: false, is_tenant_admin: false, roles: [] }),
    ).toBe(false);
  });

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

  // #2216: a tenant admin who is not a super-admin is not admitted; the
  // backend's HasUnifiedAuditLogRole SQL would 403 them anyway.
  test("redirect: roles includes 'admin' but is_admin false (tenant admin)", () => {
    expect(canReachAuditLog({ is_admin: false, roles: ["admin"] })).toBe(false);
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
