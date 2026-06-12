// Slice 674 — vitest coverage for the pure tenant-name resolution
// helpers shared by every "active tenant name" surface (dashboard H1
// `TenantContext`, the topbar `Breadcrumb`, and — by construction — the
// `useCurrentTenantName` hook that drives both).
//
// The bug slice 674 fixes is a stale-fetch-lifecycle bug, not a
// wrong-source bug: the H1 + breadcrumb already read the correct source
// (`/api/me/tenants` + the `current` flag) but fetched only on mount, so
// an in-tab tenant switch (which rotates the JWT cookie + calls
// router.refresh(), but does NOT re-run a mounted client component's
// effect) left them showing the origin tenant name. The fix routes both
// through a shared hook that re-fetches on the slice-199 `tenant-switched`
// broadcast. These pure helpers are the node-env-testable core of that
// shared resolution path (the React hook itself is exercised by the
// multi-tenant Playwright spec, per slice 069 P0-A3: no component-render
// vitest tier).

import { describe, expect, test } from "vitest";

import { parseTenantsResponse, pickCurrentTenantName } from "./current-tenant";

describe("pickCurrentTenantName", () => {
  test("returns the current tenant's name when one exists", () => {
    expect(
      pickCurrentTenantName([
        { id: "t1", name: "Acme Industries", current: true },
        { id: "t2", name: "Other Tenant", current: false },
      ]),
    ).toBe("Acme Industries");
  });

  test("returns the SWITCHED current tenant, not the first row", () => {
    // The exact regression: after a switch the `current` flag moves to a
    // non-first row. The resolver must follow the flag, never positional.
    expect(
      pickCurrentTenantName([
        { id: "t1", name: "Default Tenant", current: false },
        { id: "t2", name: "Demo Demo", current: true },
      ]),
    ).toBe("Demo Demo");
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

describe("parseTenantsResponse", () => {
  test("extracts a well-formed tenants array", () => {
    const rows = parseTenantsResponse({
      tenants: [
        { id: "t1", name: "Default Tenant", current: false },
        { id: "t2", name: "Demo Demo", current: true },
      ],
    });
    expect(rows).toEqual([
      { id: "t1", name: "Default Tenant", current: false },
      { id: "t2", name: "Demo Demo", current: true },
    ]);
  });

  test("returns null when the payload is not an object", () => {
    expect(parseTenantsResponse(null)).toBe(null);
    expect(parseTenantsResponse("nope")).toBe(null);
    expect(parseTenantsResponse(42)).toBe(null);
  });

  test("returns null when `tenants` is missing or not an array", () => {
    expect(parseTenantsResponse({})).toBe(null);
    expect(parseTenantsResponse({ tenants: "x" })).toBe(null);
  });

  test("composes with pickCurrentTenantName end-to-end", () => {
    const rows = parseTenantsResponse({
      tenants: [
        { id: "t1", name: "Default Tenant", current: false },
        { id: "t2", name: "Demo Demo", current: true },
      ],
    });
    expect(pickCurrentTenantName(rows)).toBe("Demo Demo");
  });
});
