// Slice 515 — NIST CSF 2.0 Tier / Profile assessment client lib. Server-side
// typed fetches against the platform API, used by the BFF routes under
// /api/csf/**. Mirrors the slice-018 framework-scopes lib shape.

import { apiFetch } from "./_shared";

export type CsfTier =
  | "tier1_partial"
  | "tier2_risk_informed"
  | "tier3_repeatable"
  | "tier4_adaptive";

export type CsfProfileKind = "current" | "target";

export type CsfTargetOutcome = "not_targeted" | "partial" | "largely" | "fully";

export type CsfTierRating = {
  id: string;
  framework_version_id: string;
  tier: CsfTier;
  rationale: string;
  rated_by: string;
  rated_at: string;
};

export type CsfSelection = {
  subcategory_code: string;
  subcategory_title: string;
  requirement_id: string;
  target_outcome: CsfTargetOutcome;
  note: string;
};

export type CsfProfile = {
  id: string;
  framework_version_id: string;
  kind: CsfProfileKind;
  name: string;
  created_by: string;
};

export type CsfGapRow = {
  subcategory_code: string;
  subcategory_title: string;
  requirement_id: string;
  current_outcome: CsfTargetOutcome;
  target_outcome: CsfTargetOutcome;
  gap_delta: number;
  met: boolean;
};

export type CsfGapView = {
  framework_version_id: string;
  gap: CsfGapRow[];
  gap_count: number;
  tier_rating?: CsfTierRating | null;
};

function fv(frameworkVersion: string): string {
  return `?framework_version=${encodeURIComponent(frameworkVersion)}`;
}

export async function getCsfTier(
  bearer: string,
  frameworkVersion: string,
): Promise<CsfTierRating | null> {
  const res = await apiFetch(`/v1/csf/tier${fv(frameworkVersion)}`, bearer);
  const body = (await res.json()) as { tier_rating: CsfTierRating | null };
  return body.tier_rating;
}

export async function getCsfProfile(
  bearer: string,
  frameworkVersion: string,
  kind: CsfProfileKind,
): Promise<{ profile: CsfProfile | null; selections: CsfSelection[] }> {
  const res = await apiFetch(
    `/v1/csf/profiles/${kind}${fv(frameworkVersion)}`,
    bearer,
  );
  return (await res.json()) as {
    profile: CsfProfile | null;
    selections: CsfSelection[];
  };
}

export async function getCsfGap(
  bearer: string,
  frameworkVersion: string,
): Promise<CsfGapView> {
  const res = await apiFetch(`/v1/csf/gap${fv(frameworkVersion)}`, bearer);
  return (await res.json()) as CsfGapView;
}
