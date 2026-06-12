// Slice 223 — vitest coverage for the pure helper that maps URL
// pathnames to user-visible page names. Powers the shared-shell
// breadcrumb's right-hand segment.
//
// The breadcrumb component itself is a server component that integrates
// the Next.js cookie/header machinery with the `/v1/me/tenants` fetch,
// which is hostile to a vitest unit test. The pure derivation is
// extracted here and pinned via table-driven cases. Mirrors the slice
// 213 split (`web/lib/display-name.test.ts`) and slice 271 spillover
// AC-3/4.

import { describe, expect, test } from "vitest";

import { derivePageName } from "./page-names";

describe("derivePageName", () => {
  test("returns 'Dashboard' for /dashboard", () => {
    expect(derivePageName("/dashboard")).toBe("Dashboard");
  });

  test("returns 'Controls' for /controls", () => {
    expect(derivePageName("/controls")).toBe("Controls");
  });

  test("returns 'Controls' for /controls/<id> (detail page rolls up)", () => {
    expect(derivePageName("/controls/abc-123")).toBe("Controls");
  });

  test("returns 'Evidence' for /evidence", () => {
    expect(derivePageName("/evidence")).toBe("Evidence");
  });

  test("returns 'Risks' for /risks", () => {
    expect(derivePageName("/risks")).toBe("Risks");
  });

  test("returns 'Risks' for /risks/hierarchy (subroute rolls up to the section)", () => {
    expect(derivePageName("/risks/hierarchy")).toBe("Risks");
  });

  test("returns 'Audits' for /audits", () => {
    expect(derivePageName("/audits")).toBe("Audits");
  });

  test("returns 'Audits' for /audits/new", () => {
    expect(derivePageName("/audits/new")).toBe("Audits");
  });

  test("returns 'Policies' for /policies", () => {
    expect(derivePageName("/policies")).toBe("Policies");
  });

  test("returns 'Vendors' for /vendors", () => {
    expect(derivePageName("/vendors")).toBe("Vendors");
  });

  test("returns 'Board Packs' for /board-packs", () => {
    expect(derivePageName("/board-packs")).toBe("Board Packs");
  });

  // ATLAS-011 — the Vendor Claims module lives under /oscal/*; without an
  // explicit map entry the breadcrumb humanized the raw segment to the
  // jargon "Oscal". Map it to the user-facing module name.
  test("returns 'Vendor Claims' for /oscal/component-definitions", () => {
    expect(derivePageName("/oscal/component-definitions")).toBe(
      "Vendor Claims",
    );
  });

  test("returns 'Catalog' for /catalog/scf", () => {
    expect(derivePageName("/catalog/scf")).toBe("Catalog");
  });

  test("returns 'Settings' for /settings", () => {
    expect(derivePageName("/settings")).toBe("Settings");
  });

  test("returns 'Admin' for /admin", () => {
    expect(derivePageName("/admin")).toBe("Admin");
  });

  test("returns 'Activity' for /activity", () => {
    expect(derivePageName("/activity")).toBe("Activity");
  });

  test("returns 'Calendar' for /calendar", () => {
    expect(derivePageName("/calendar")).toBe("Calendar");
  });

  test("returns 'Metrics' for /dashboards/metrics", () => {
    expect(derivePageName("/dashboards/metrics")).toBe("Metrics");
  });

  test("returns 'Audit log' for /audit-log", () => {
    expect(derivePageName("/audit-log")).toBe("Audit log");
  });

  test("humanizes unknown first segment as fallback", () => {
    expect(derivePageName("/some-new-thing")).toBe("Some new thing");
  });

  test("humanizes unknown camelCase first segment as fallback", () => {
    expect(derivePageName("/somenewthing")).toBe("Somenewthing");
  });

  test("returns empty string for the root path", () => {
    expect(derivePageName("/")).toBe("");
  });

  test("returns empty string for the empty path", () => {
    expect(derivePageName("")).toBe("");
  });

  test("strips a leading slash + trailing slash uniformly", () => {
    expect(derivePageName("/controls/")).toBe("Controls");
  });

  test("ignores query strings", () => {
    expect(derivePageName("/controls?framework=soc2")).toBe("Controls");
  });

  test("ignores hash fragments", () => {
    expect(derivePageName("/controls#top")).toBe("Controls");
  });
});
