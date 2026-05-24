// Slice 223 — vitest coverage for the pure helper that powers the
// breadcrumb's left-hand segment.
//
// The integrated `<Breadcrumb />` component reads `usePathname()` and
// `/api/me/tenants` — hostile to a node-env vitest unit test (no
// jsdom, no React testing library — see slice 069 P0-A3). The pure
// derivation is extracted here. Mirrors the slice 213 pattern of
// pinning `pickMostRecentInProgress`.

import { describe, expect, test } from "vitest";

import { pickCurrentTenantName } from "./breadcrumb";

describe("pickCurrentTenantName", () => {
  test("returns the current tenant's name when one exists", () => {
    expect(
      pickCurrentTenantName([
        { id: "t1", name: "Acme Industries", current: true },
        { id: "t2", name: "Other Tenant", current: false },
      ]),
    ).toBe("Acme Industries");
  });

  test("returns null when no tenant is marked current", () => {
    expect(
      pickCurrentTenantName([
        { id: "t1", name: "Acme Industries", current: false },
        { id: "t2", name: "Other Tenant", current: false },
      ]),
    ).toBe(null);
  });

  test("returns null on empty list", () => {
    expect(pickCurrentTenantName([])).toBe(null);
  });

  test("returns null on null input (defensive — fetch failed)", () => {
    expect(pickCurrentTenantName(null)).toBe(null);
  });

  test("returns null when current tenant's name is whitespace-only", () => {
    expect(
      pickCurrentTenantName([{ id: "t1", name: "   ", current: true }]),
    ).toBe(null);
  });

  test("trims surrounding whitespace from the returned name", () => {
    expect(
      pickCurrentTenantName([{ id: "t1", name: "  Acme  ", current: true }]),
    ).toBe("Acme");
  });
});
