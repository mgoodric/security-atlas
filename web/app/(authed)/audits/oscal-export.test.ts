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
  oscalExportDownloadFilename,
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

  // The `download` attribute VALUE is the regression guard for the
  // slice-457 e2e failure: a bare `download` attribute made the browser
  // derive "oscal-export.txt" from the URL (same-origin precedence over
  // the server Content-Disposition). The builder pins the deterministic
  // filename. It MIRRORS the Go-side downloadFilename/frozenDate.
  describe("oscalExportDownloadFilename (anchor download-attr value)", () => {
    const PID = "00000000-0000-0000-0000-0000000457bb";

    it("RFC-3339 frozen_at -> oscal-bundle-<id>-<YYYY-MM-DD>.json", () => {
      expect(oscalExportDownloadFilename(PID, "2026-03-31T00:00:00Z")).toBe(
        `oscal-bundle-${PID}-2026-03-31.json`,
      );
    });

    it("date-only frozen_at -> date segment present", () => {
      expect(oscalExportDownloadFilename(PID, "2026-03-31")).toBe(
        `oscal-bundle-${PID}-2026-03-31.json`,
      );
    });

    it("null/undefined/empty frozen_at -> date omitted, never guessed", () => {
      expect(oscalExportDownloadFilename(PID, null)).toBe(
        `oscal-bundle-${PID}.json`,
      );
      expect(oscalExportDownloadFilename(PID, undefined)).toBe(
        `oscal-bundle-${PID}.json`,
      );
      expect(oscalExportDownloadFilename(PID, "")).toBe(
        `oscal-bundle-${PID}.json`,
      );
    });

    it("malformed frozen_at (too short / wrong separators) -> date omitted", () => {
      expect(oscalExportDownloadFilename(PID, "2026-03")).toBe(
        `oscal-bundle-${PID}.json`,
      );
      expect(oscalExportDownloadFilename(PID, "2026/03/31T00:00")).toBe(
        `oscal-bundle-${PID}.json`,
      );
    });

    it("hyphens-OK but stray time char in the 10-char window -> date omitted", () => {
      // Passes the position-4/7 hyphen check but the leading 10 chars
      // carry a `T` (a malformed short date like "2026-03-3T...") — the
      // ` :TZ` guard rejects it rather than emitting a half-date.
      expect(oscalExportDownloadFilename(PID, "2026-03-3T12:00:00Z")).toBe(
        `oscal-bundle-${PID}.json`,
      );
    });

    it("always ends in .json (never the browser's sniffed .txt fallback)", () => {
      expect(oscalExportDownloadFilename(PID, "2026-03-31T00:00:00Z")).toMatch(
        /\.json$/,
      );
      expect(oscalExportDownloadFilename(PID, null)).toMatch(/\.json$/);
    });
  });
});
