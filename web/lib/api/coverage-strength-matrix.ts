// OE-473 / slice 402a — cross-framework coverage-strength matrix contract.
//
// Backend source: GET /v1/coverage-strength/matrix. This is a read-model
// contract for the slice 402b matrix UI; no UI derives scores or maps
// bands to colors locally. The backend returns both the numeric
// coverage_strength and the semantic status token the cell should use.

import { apiFetch, bffControlFetch } from "./_shared";
import type { RequirementConfidenceBand } from "./requirement-coverage";

export type CoverageStrengthBandToken =
  | "pass"
  | "warning"
  | "critical"
  | "info";

export type CoverageStrengthBandMapping = {
  band: RequirementConfidenceBand;
  token: CoverageStrengthBandToken;
  label: string;
};

export type CoverageStrengthMatrixFramework = {
  framework_version_id: string;
  framework_slug: string;
  framework_name: string;
  version: string;
  status: string;
};

export type CoverageStrengthMatrixCell = {
  framework_version_id: string;
  coverage_strength: number;
  confidence_band: RequirementConfidenceBand;
  band_token: CoverageStrengthBandToken;
  requirement_count: number;
  contributing: boolean;
};

export type CoverageStrengthMatrixRow = {
  anchor: {
    id: string;
    scf_id: string;
    family: string;
    name: string;
    description?: string;
  };
  cells: CoverageStrengthMatrixCell[];
};

export type CoverageStrengthMatrix = {
  axis: {
    rows: "scf_anchor";
    columns: "framework_current_version";
    cell_value: "max_requirement_contribution";
    aggregation: "max(edge_strength * anchor_coverage)";
  };
  bands: CoverageStrengthBandMapping[];
  frameworks: CoverageStrengthMatrixFramework[];
  rows: CoverageStrengthMatrixRow[];
  limit: number;
  offset: number;
};

export const COVERAGE_STRENGTH_BAND_TOKENS: Record<
  RequirementConfidenceBand,
  CoverageStrengthBandToken
> = {
  strong: "pass",
  partial: "warning",
  weak: "critical",
  uncovered: "info",
};

export async function getCoverageStrengthMatrix(
  bearer: string,
  search = "",
): Promise<CoverageStrengthMatrix> {
  const suffix = search ? `?${search}` : "";
  const res = await apiFetch(`/v1/coverage-strength/matrix${suffix}`, bearer);
  return (await res.json()) as CoverageStrengthMatrix;
}

export function fetchCoverageStrengthMatrix(
  search = "",
): Promise<CoverageStrengthMatrix> {
  const suffix = search ? `?${search}` : "";
  return bffControlFetch<CoverageStrengthMatrix>(
    `/api/coverage-strength/matrix${suffix}`,
  );
}
