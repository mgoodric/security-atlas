// Slice 186 — unit coverage for the sidebar's "Admin" entry visibility
// predicate.
//
// `shouldShowAdminEntry` is exported as pure logic so the slice-069
// no-JSX-rendering constraint (P0-A3 on `web/vitest.config.ts`) is
// honored. The Sidebar component itself is a server component whose
// fetch + render surface is exercised by the Playwright spec at
// `web/e2e/admin-bootstrap.spec.ts` (AC-3) — vitest doesn't model
// server-component rendering, so the predicate carries the unit
// burden.
//
// Matrix (AC-5 — `roles=[user]` hides, `roles=[admin]` shows — is the
// load-bearing pair; the rest defends P0-186-4 fail-closed):
//
//   * is_admin true                                          -> SHOW
//   * is_admin true, roles=[]                                 -> SHOW (flag wins)
//   * is_admin true, roles=["viewer"]                         -> SHOW (flag wins)
//   * is_admin false, roles=["admin"]                         -> SHOW (AC-5 positive)
//   * is_admin false, roles=["super_admin"]                   -> SHOW
//   * is_admin false, roles=["tenant_admin"]                  -> SHOW
//   * is_admin false, roles=["admin","viewer"]                -> SHOW (one matching is enough)
//   * is_admin false, roles=["user"]                          -> HIDE (AC-5 negative)
//   * is_admin false, roles=["viewer"]                        -> HIDE
//   * is_admin false, roles=["control_owner"]                 -> HIDE
//   * is_admin false, roles=["grc_engineer"]                  -> HIDE (canonical role but not admin scope)
//   * is_admin false, roles=["auditor"]                       -> HIDE
//   * is_admin false, roles=[]                                -> HIDE
//   * is_admin false, roles missing                           -> HIDE (P0-186-4)
//   * is_admin false, roles=null                              -> HIDE (P0-186-4)
//   * is_admin false, roles="admin" (string not array)        -> HIDE (P0-186-4)
//   * empty body                                              -> HIDE (P0-186-4)
//   * is_admin non-bool truthy (string "true")                -> HIDE  (strict equality)
//   * roles contains non-strings, no admin match               -> HIDE
//   * roles contains non-strings AND one admin match           -> SHOW

import { describe, expect, test } from "vitest";

import { shouldShowAdminEntry } from "./admin-nav";

describe("shouldShowAdminEntry", () => {
  test("show: is_admin true overrides roles entirely", () => {
    expect(shouldShowAdminEntry({ is_admin: true })).toBe(true);
    expect(shouldShowAdminEntry({ is_admin: true, roles: [] })).toBe(true);
    expect(shouldShowAdminEntry({ is_admin: true, roles: ["viewer"] })).toBe(
      true,
    );
  });

  test("show: roles=['admin'] (AC-5 positive)", () => {
    expect(shouldShowAdminEntry({ is_admin: false, roles: ["admin"] })).toBe(
      true,
    );
  });

  test("show: roles=['super_admin']", () => {
    expect(
      shouldShowAdminEntry({ is_admin: false, roles: ["super_admin"] }),
    ).toBe(true);
  });

  test("show: roles=['tenant_admin']", () => {
    expect(
      shouldShowAdminEntry({ is_admin: false, roles: ["tenant_admin"] }),
    ).toBe(true);
  });

  test("show: one admin role among many is enough", () => {
    expect(
      shouldShowAdminEntry({
        is_admin: false,
        roles: ["viewer", "admin", "control_owner"],
      }),
    ).toBe(true);
  });

  test("hide: roles=['user'] (AC-5 negative)", () => {
    expect(shouldShowAdminEntry({ is_admin: false, roles: ["user"] })).toBe(
      false,
    );
  });

  test("hide: roles=['viewer']", () => {
    expect(shouldShowAdminEntry({ is_admin: false, roles: ["viewer"] })).toBe(
      false,
    );
  });

  test("hide: roles=['control_owner']", () => {
    expect(
      shouldShowAdminEntry({ is_admin: false, roles: ["control_owner"] }),
    ).toBe(false);
  });

  test("hide: roles=['grc_engineer'] (canonical role, NOT admin scope)", () => {
    expect(
      shouldShowAdminEntry({ is_admin: false, roles: ["grc_engineer"] }),
    ).toBe(false);
  });

  test("hide: roles=['auditor']", () => {
    expect(shouldShowAdminEntry({ is_admin: false, roles: ["auditor"] })).toBe(
      false,
    );
  });

  test("hide: empty roles array", () => {
    expect(shouldShowAdminEntry({ is_admin: false, roles: [] })).toBe(false);
  });

  test("hide: roles field missing (P0-186-4 fail-closed)", () => {
    expect(shouldShowAdminEntry({ is_admin: false })).toBe(false);
  });

  test("hide: roles is null (P0-186-4 fail-closed)", () => {
    expect(shouldShowAdminEntry({ is_admin: false, roles: null })).toBe(false);
  });

  test("hide: roles is a string, not an array (P0-186-4 fail-closed)", () => {
    expect(shouldShowAdminEntry({ is_admin: false, roles: "admin" })).toBe(
      false,
    );
  });

  test("hide: empty body (everything undefined — P0-186-4 fail-closed)", () => {
    expect(shouldShowAdminEntry({})).toBe(false);
  });

  test("hide: is_admin truthy but non-bool (strict equality only)", () => {
    expect(shouldShowAdminEntry({ is_admin: "true" })).toBe(false);
    expect(shouldShowAdminEntry({ is_admin: 1 })).toBe(false);
  });

  test("hide: roles contains non-string entries, no admin string", () => {
    expect(
      shouldShowAdminEntry({
        is_admin: false,
        roles: [42, null, { admin: true }, "viewer"],
      }),
    ).toBe(false);
  });

  test("show: roles contains non-string entries plus one admin string", () => {
    expect(
      shouldShowAdminEntry({
        is_admin: false,
        roles: [42, null, "admin"],
      }),
    ).toBe(true);
  });
});
