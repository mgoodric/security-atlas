// Slice 750 — portfolio / multi-control AI evidence-summary client.
//
// GET /v1/evidence-summary/portfolio returns the DETERMINISTIC TWO-LEVEL bounded
// cross-control rollup ALWAYS (cap controls-per-summary AND records-per-control),
// plus a NON-BINDING, cited, local-default-Ollama summary of that rollup when one
// is available AND every citation resolves to a tenant-owned row in the
// cross-control grounding set AND every numeric claim matches the deterministic
// rollup. The summary is null when suppressed (graceful degradation); the rollup
// is always present. The summary is a comprehension aid on the dashboard — never
// an audit artifact, with no approve/publish/export affordance. Cross-control
// sibling of slice 502's single-control evidence-summary.

import { apiFetch, bffControlFetch } from "./_shared";
import type {
  EvidenceSummaryBody,
  EvidenceSummaryFact,
} from "./control-detail";

// PortfolioControlRollup is one control's slice of the deterministic
// cross-control rollup. `showing` of `total` live records, bounded per control.
export type PortfolioControlRollup = {
  control_id: string;
  control_title: string;
  showing: number;
  total: number;
  records: EvidenceSummaryFact[];
};

// PortfolioRollup is the deterministic portfolio rollup the summary's numeric
// claims were verified against (AC-3). The UI renders these counts from ground
// truth, never from the model.
export type PortfolioRollup = {
  controls_in_summary: number;
  total_matched: number;
  controls_with_evidence: number;
  controls_without_evidence: number;
  total_records: number;
};

// PortfolioEvidenceSet is the deterministic TWO-LEVEL bounded rollup envelope.
// `mode` names the filter dimension; `controls_per_summary` /
// `records_per_control` expose BOTH bounds for honest UI labeling (AC-5).
export type PortfolioEvidenceSet = {
  mode: "program" | "family" | "framework";
  family?: string;
  framework_label?: string;
  live_only: boolean;
  controls_per_summary: number;
  records_per_control: number;
  rollup: PortfolioRollup;
  controls: PortfolioControlRollup[];
};

export type PortfolioEvidenceSummaryResponse = {
  evidence: PortfolioEvidenceSet;
  // null when the summary was suppressed (generation unavailable, no evidence, a
  // citation failed to resolve, or a numeric claim did not match the rollup) —
  // the deterministic rollup still renders.
  summary: EvidenceSummaryBody | null;
  suppressed_reason: string;
};

// PortfolioFilter is the optional filter the dashboard card passes. At most one
// dimension in v1; an empty filter is the whole-program rollup.
export type PortfolioFilter = {
  family?: string;
  frameworkVersionID?: string;
  frameworkLabel?: string;
};

function portfolioQuery(filter?: PortfolioFilter): string {
  if (!filter) return "";
  const params = new URLSearchParams();
  if (filter.frameworkVersionID) {
    params.set("framework_version_id", filter.frameworkVersionID);
    if (filter.frameworkLabel) params.set("framework", filter.frameworkLabel);
  } else if (filter.family) {
    params.set("family", filter.family);
  }
  const q = params.toString();
  return q ? `?${q}` : "";
}

// getPortfolioEvidenceSummary is the server-side fetch (BFF -> upstream). The
// bearer never reaches the client.
export async function getPortfolioEvidenceSummary(
  bearer: string,
  filter?: PortfolioFilter,
): Promise<PortfolioEvidenceSummaryResponse> {
  const res = await apiFetch(
    `/v1/evidence-summary/portfolio${portfolioQuery(filter)}`,
    bearer,
  );
  return (await res.json()) as PortfolioEvidenceSummaryResponse;
}

// fetchPortfolioEvidenceSummary is the browser-side fetch (hits the BFF under
// /api/dashboard/portfolio-summary).
export function fetchPortfolioEvidenceSummary(
  filter?: PortfolioFilter,
): Promise<PortfolioEvidenceSummaryResponse> {
  return bffControlFetch<PortfolioEvidenceSummaryResponse>(
    `/api/dashboard/portfolio-summary${portfolioQuery(filter)}`,
  );
}
