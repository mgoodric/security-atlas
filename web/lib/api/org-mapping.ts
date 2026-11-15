// #1800 — the provenance an overlay edge ("org mapping") carries on the
// coverage and traversal wires.
//
// OverlayProvenance mirrors `overlayWire` in
// internal/api/ucfcoverage/handlers.go. The platform sets it only on a row
// whose `origin` is "overlay"; a catalog row never carries it.
export type OverlayProvenance = {
  source_ref: string;
  created_by: string;
  created_at: string;
};

// EdgeOrigin mirrors the `origin` column of effective_fw_to_scf_edges
// (#1798): where a requirement -> anchor path comes from.
export type EdgeOrigin = "catalog" | "overlay";

// isOrgMapping reads the platform's own mark. The UI never infers an org
// mapping from anything else (source_attribution, mapping_tier): a catalog
// edge can carry `org_internal` too.
export function isOrgMapping(row: { origin?: string }): boolean {
  return row.origin === "overlay";
}
