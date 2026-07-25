// Slice 482 — requirement coverage view binding.
//
// Binds GET /v1/requirements/{id}/coverage (slice 008 forward traversal,
// extended by slice 482 with the additive `coverage_strength` +
// `confidence_band` rollup). The endpoint returns the global catalog
// requirement + its SCF anchors (each with the STRM edge strength) and
// the tenant's RLS-scoped controls, plus the server-computed rollup. The
// frontend NEVER computes coverage_strength itself (P0-482-4) — it
// renders the backend's number and band verbatim.

import { apiFetch, bffControlFetch } from "./_shared";

// RequirementAnchorWire mirrors `anchorWire` in
// internal/api/ucfcoverage/handlers.go (the edge-bearing form returned by
// RequirementCoverage). `relationship_type` is an open STRM string by
// design (the DB enum has five values; render verbatim).
export type RequirementAnchorWire = {
  id: string;
  scf_id: string;
  family: string;
  name: string;
  description?: string;
  edge_id?: string;
  relationship_type?: string;
  strength?: number;
  source_attribution?: string;
  rationale?: string;
};

// RequirementControlWire mirrors `controlWire` (the RLS-scoped controls[]
// rows) in the same handler.
export type RequirementControlWire = {
  id: string;
  bundle_id: string;
  version: number;
  scf_id?: string;
  scf_anchor_id?: string;
  title: string;
  control_family: string;
  implementation_type: string;
  owner_role: string;
  lifecycle_state: string;
  freshness_class?: string;
};

// ConfidenceBand mirrors the Go ConfidenceBand vocabulary
// (internal/api/ucfcoverage/rollup.go). Open-ended union with a string
// fallback would be over-defensive; the backend enum is closed.
export type RequirementConfidenceBand =
  | "uncovered"
  | "weak"
  | "partial"
  | "strong";

// RequirementCoverage mirrors the slice 482 response. The two rollup
// fields are ALWAYS present (additive, never omitempty on the wire):
//   - coverage_strength: server-computed best-satisfying-path score in
//     [0, 1] over the requirement's anchors × the tenant's evaluated
//     state. 0 when the tenant has no in-scope evaluated coverage.
//   - confidence_band: the named bucket the score falls into.
export type RequirementCoverage = {
  requirement: {
    id: string;
    code: string;
    title: string;
    body?: string;
  };
  anchors: RequirementAnchorWire[];
  controls: RequirementControlWire[];
  coverage_strength: number;
  confidence_band: RequirementConfidenceBand;
};

// ----- server-side fn (called by the BFF route handler) -----

export async function getRequirementCoverage(
  bearer: string,
  requirementID: string,
): Promise<RequirementCoverage> {
  const res = await apiFetch(
    `/v1/requirements/${encodeURIComponent(requirementID)}/coverage`,
    bearer,
  );
  return (await res.json()) as RequirementCoverage;
}

// ----- browser-side fn (hits the BFF under /api/requirements/**) -----

export function fetchRequirementCoverage(
  requirementID: string,
): Promise<RequirementCoverage> {
  return bffControlFetch<RequirementCoverage>(
    `/api/requirements/${encodeURIComponent(requirementID)}/coverage`,
  );
}
