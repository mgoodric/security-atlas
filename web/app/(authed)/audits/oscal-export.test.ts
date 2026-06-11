// Slice 457 — unit coverage for the OSCAL signed-export download surface
// constants (supersedes the slice-217 future-state disclosure test).
//
// Pure-data tests for the constants + URL builder exported by
// `./oscal-export`. The DOM-level contract (the audits page renders a
// working per-frozen-period download link) is covered by the Playwright
// spec at `web/e2e/audits-list.spec.ts` (AC-3) and the download e2e at
// `web/e2e/oscal-export-e2e.spec.ts`.
//
// Test environment is node-env, no JSX (per `web/vitest.config.ts`).

import { describe, expect, it } from "vitest";

import {
  OSCAL_EXPORT_DOWNLOAD_LABEL,
  OSCAL_EXPORT_DOWNLOAD_TESTID,
  OSCAL_EXPORT_TOOLBAR_NOTE,
  OSCAL_EXPORT_TOOLBAR_TESTID,
  oscalExportDownloadURL,
} from "./oscal-export";

describe("OSCAL export download surface (slice 457)", () => {
  it("exposes stable data-testid tokens", () => {
    expect(OSCAL_EXPORT_DOWNLOAD_TESTID).toBe("audits-oscal-export-download");
    expect(OSCAL_EXPORT_TOOLBAR_TESTID).toBe("audits-oscal-export-toolbar");
  });

  it("download label is the working action verb (no longer a coming-soon disclosure)", () => {
    // Slice 217 migrated: the label is now an action, and must NOT frame
    // the capability as future/broken (the honesty property migrates to
    // the live affordance — AC-5).
    expect(OSCAL_EXPORT_DOWNLOAD_LABEL).toBe("Export OSCAL bundle");
    const lc = OSCAL_EXPORT_DOWNLOAD_LABEL.toLowerCase();
    expect(lc).not.toMatch(/coming/);
    expect(lc).not.toMatch(/future/);
    expect(lc).not.toMatch(/disabled/);
  });

  it("toolbar note describes the live capability and names the frozen-period home", () => {
    expect(OSCAL_EXPORT_TOOLBAR_NOTE.length).toBeGreaterThan(0);
    expect(OSCAL_EXPORT_TOOLBAR_NOTE.toLowerCase()).toMatch(/frozen period/);
    // Honesty discipline: no failure-framing, no placeholder slice number.
    const lc = OSCAL_EXPORT_TOOLBAR_NOTE.toLowerCase();
    expect(lc).not.toMatch(/coming/);
    expect(lc).not.toMatch(/unavailable/);
    expect(OSCAL_EXPORT_TOOLBAR_NOTE).not.toMatch(/#\d+/);
    expect(lc).not.toMatch(/slice \d+/);
  });

  it("builds the BFF download URL for a period id", () => {
    expect(oscalExportDownloadURL("abc-123")).toBe(
      "/api/audits/abc-123/oscal-export",
    );
  });

  it("percent-encodes the period id so a hostile id cannot escape the path segment", () => {
    const url = oscalExportDownloadURL("../../etc/passwd");
    expect(url).toBe("/api/audits/..%2F..%2Fetc%2Fpasswd/oscal-export");
    // The encoded id never re-introduces a raw slash into the segment.
    expect(url.split("/oscal-export")[0]).not.toContain("etc/passwd");
  });
});
