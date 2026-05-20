// Slice 136 — risk-register data-export client helpers.
//
// Builds the BFF URL the browser hits to trigger a download. The
// backend authors the filename via Content-Disposition; the URL only
// carries the format selector. Mirrors the slice 135 audit-log
// `buildAuditLogExportURL` shape so future per-entity export helpers
// land at the same call-site shape.

// Wire format identifiers. PDF is intentionally not in the list; the
// three formats below match the backend's `internal/export.AllFormats`
// exactly.
export const RISK_EXPORT_FORMATS = ["csv", "json", "xlsx"] as const;
export type RiskExportFormat = (typeof RISK_EXPORT_FORMATS)[number];

/**
 * buildRiskExportURL returns the BFF URL the browser navigates to in
 * order to trigger a download in the requested format. The risk
 * register has no required filter params at v1 — `?format=` is the
 * only query string segment. Exported so the Playwright spec (and
 * the vitest unit suite) can assert the URL the Export button
 * assembles.
 */
export function buildRiskExportURL(format: RiskExportFormat): string {
  const u = new URLSearchParams();
  u.set("format", format);
  return `/api/risks/export?${u.toString()}`;
}

// Human-readable labels for the format dropdown / button group.
export const RISK_EXPORT_FORMAT_LABELS: Record<RiskExportFormat, string> = {
  csv: "CSV",
  json: "JSON",
  xlsx: "XLSX",
};
