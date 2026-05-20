// Slice 177 — exceptions data-export client helpers.
//
// Builds the BFF URL the browser hits to trigger an exceptions download.
// The backend authors the filename via Content-Disposition; the URL only
// carries the format selector. Mirrors the slice 137 controls-export
// helper shape (CONTROLS_EXPORT_FORMATS / buildControlsExportURL) so
// per-entity export helpers land at the same call-site shape.
//
// The underlying BFF (`/api/admin/exceptions/export`) and the platform
// handler (`/v1/admin/exceptions/export`) were both shipped by slice
// 138; this slice merely surfaces them in the canonical list-view UI.

// Wire format identifiers. PDF is intentionally not in the list; the
// three formats below match the backend's `internal/export.AllFormats`
// exactly (and the slice 138 export handler accepts the same triple).
export const EXCEPTIONS_EXPORT_FORMATS = ["csv", "json", "xlsx"] as const;
export type ExceptionsExportFormat = (typeof EXCEPTIONS_EXPORT_FORMATS)[number];

/**
 * buildExceptionsExportURL returns the BFF URL the browser navigates to
 * in order to trigger a download in the requested format. The
 * exceptions export has no required filter params — `?format=` is the
 * only query string segment. Exported so the Playwright spec (and the
 * vitest unit suite) can assert the URL the Export button assembles.
 */
export function buildExceptionsExportURL(
  format: ExceptionsExportFormat,
): string {
  const u = new URLSearchParams();
  u.set("format", format);
  return `/api/admin/exceptions/export?${u.toString()}`;
}

// Human-readable labels for the format buttons.
export const EXCEPTIONS_EXPORT_FORMAT_LABELS: Record<
  ExceptionsExportFormat,
  string
> = {
  csv: "CSV",
  json: "JSON",
  xlsx: "XLSX",
};
