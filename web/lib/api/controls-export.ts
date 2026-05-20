// Slice 137 — controls UCF graph data-export client helpers.
//
// Builds the BFF URL the browser hits to trigger a controls-catalog
// download. The backend authors the filename via Content-Disposition;
// the URL only carries the format selector. Mirrors the slice 135 /
// 136 / 139 helper shape so future per-entity export helpers land at
// the same call-site shape.

// Wire format identifiers. PDF is intentionally not in the list; the
// three formats below match the backend's `internal/export.AllFormats`
// exactly.
export const CONTROLS_EXPORT_FORMATS = ["csv", "json", "xlsx"] as const;
export type ControlsExportFormat = (typeof CONTROLS_EXPORT_FORMATS)[number];

/**
 * buildControlsExportURL returns the BFF URL the browser navigates to
 * in order to trigger a download in the requested format. The
 * controls export has no required filter params at v1 — `?format=` is
 * the only query string segment. Exported so the Playwright spec
 * (and the vitest unit suite) can assert the URL the Export button
 * assembles.
 */
export function buildControlsExportURL(format: ControlsExportFormat): string {
  const u = new URLSearchParams();
  u.set("format", format);
  return `/api/controls/export?${u.toString()}`;
}

// Human-readable labels for the format buttons.
export const CONTROLS_EXPORT_FORMAT_LABELS: Record<
  ControlsExportFormat,
  string
> = {
  csv: "CSV",
  json: "JSON",
  xlsx: "XLSX",
};
