// Slice 174 — UCF anchor catalog data-export client helpers.
//
// Builds the BFF URL the browser hits to trigger an anchor-catalog
// download. The backend authors the filename via Content-Disposition;
// the URL only carries the format selector. Mirrors the slice 137 /
// 138 / 139 helper shape so future per-entity export helpers land at
// the same call-site shape.

// Wire format identifiers — match the backend's `internal/export.AllFormats`.
export const ANCHORS_EXPORT_FORMATS = ["csv", "json", "xlsx"] as const;
export type AnchorsExportFormat = (typeof ANCHORS_EXPORT_FORMATS)[number];

/**
 * buildAnchorsExportURL returns the BFF URL the browser navigates to
 * in order to trigger a download in the requested format. The
 * anchors export has no required filter params at v1 — `?format=` is
 * the only query string segment.
 */
export function buildAnchorsExportURL(format: AnchorsExportFormat): string {
  const u = new URLSearchParams();
  u.set("format", format);
  return `/api/anchors/export?${u.toString()}`;
}

// Human-readable labels for the format buttons.
export const ANCHORS_EXPORT_FORMAT_LABELS: Record<AnchorsExportFormat, string> =
  {
    csv: "CSV",
    json: "JSON",
    xlsx: "XLSX",
  };
