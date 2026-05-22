// Slice 175 — controls history-export client helpers.
//
// Builds the BFF URL the browser hits to trigger a controls-history
// download (lineage view including superseded versions). The backend
// authors the filename via Content-Disposition; the URL only carries
// the format selector. Mirrors the slice 137 controls-export helper
// shape exactly so future per-entity export helpers land at the same
// call-site shape.

// Wire format identifiers. PDF is intentionally not in the list; the
// three formats below match the backend's `internal/export.AllFormats`
// exactly.
export const CONTROLS_HISTORY_EXPORT_FORMATS = ["csv", "json", "xlsx"] as const;
export type ControlsHistoryExportFormat =
  (typeof CONTROLS_HISTORY_EXPORT_FORMATS)[number];

/**
 * buildControlsHistoryExportURL returns the BFF URL the browser
 * navigates to in order to trigger a download of the controls history
 * (lineage including superseded versions) in the requested format.
 * Exported so the Playwright spec (and the vitest unit suite) can
 * assert the URL the Export-History button assembles.
 */
export function buildControlsHistoryExportURL(
  format: ControlsHistoryExportFormat,
): string {
  const u = new URLSearchParams();
  u.set("format", format);
  return `/api/controls/history/export?${u.toString()}`;
}

// Human-readable labels for the format buttons.
export const CONTROLS_HISTORY_EXPORT_FORMAT_LABELS: Record<
  ControlsHistoryExportFormat,
  string
> = {
  csv: "CSV",
  json: "JSON",
  xlsx: "XLSX",
};
