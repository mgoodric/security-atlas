// Slice 536b — the crosswalk review/edit client vocabulary.
//
// These types mirror the wire shapes in
// internal/api/admincrosswalkreview/wire.go (the review queue + content edit +
// audit trail) and internal/api/admincrosswalktier/http.go (slice 483's tier
// transition, which is the approve/reject action this UI calls — it does NOT
// have one of its own).
//
// The two axes stay separate here exactly as they are on the backend
// (ADR 0018 / slice 483 P0-483-3): `source_attribution` is import-time
// provenance the reviewer can never change, `mapping_tier` is the trust state
// the reviewer moves through slice 483's state machine.

import { bffControlFetch } from "./_shared";
import { APIError } from "./base";

// The five NIST IR 8477 STRM relationship types (canvas §3.2). Editable.
export const RELATIONSHIP_TYPES = [
  "equal",
  "subset_of",
  "superset_of",
  "intersects_with",
  "no_relationship",
] as const;
export type RelationshipType = (typeof RELATIONSHIP_TYPES)[number];

// The four trust tiers (slice 483). NOT editable through the edit form — a
// tier moves only through the tier endpoint, and only a human moves it.
export type MappingTier = "draft" | "under_review" | "verified" | "rejected";

export type ConflictSeverity = "low" | "medium" | "high";

export type CrosswalkEdge = {
  id: string;
  anchor_id: string;
  anchor_scf_id: string;
  anchor_family: string;
  anchor_title: string;
  relationship_type: RelationshipType;
  strength: number;
  source_attribution: string;
  mapping_tier: MappingTier;
  rationale: string;
  updated_at: string;
};

export type CrosswalkConflict = {
  kind: string;
  reason: string;
  severity: ConflictSeverity;
  edge_ids: string[];
  anchor_scf_ids: string[];
  detail: string;
};

export type CrosswalkReviewRequirement = {
  id: string;
  code: string;
  title: string;
  edges: CrosswalkEdge[];
  conflicts: CrosswalkConflict[];
};

export type CrosswalkReviewQueue = {
  framework_version_id: string;
  requirements: CrosswalkReviewRequirement[];
  conflict_count: number;
  total: number;
  limit: number;
  offset: number;
};

export type CrosswalkContent = {
  relationship_type: RelationshipType;
  strength: number;
  rationale: string;
};

export type CrosswalkEditResponse = {
  edge_id: string;
  edit_id: string;
  from: CrosswalkContent;
  to: CrosswalkContent;
  note?: string;
  editor_id: string;
  // Present only when the edit demoted a verified mapping back to under_review
  // (slice 536b D-536b-1). The UI surfaces it so a reviewer is never surprised
  // that their edit cost the mapping its verified badge.
  tier_demoted_from?: string;
  tier_demoted_to?: string;
  created_at: string;
};

export type CrosswalkContentEdit = {
  id: string;
  editor_id: string;
  from: CrosswalkContent;
  to: CrosswalkContent;
  note?: string;
  created_at: string;
};

export type CrosswalkTierTransition = {
  reviewer_id: string;
  from_tier: MappingTier;
  to_tier: MappingTier;
  note?: string;
  created_at: string;
};

export type CrosswalkEdgeAudit = {
  edge_id: string;
  content_edits: CrosswalkContentEdit[];
  tier_transitions: CrosswalkTierTransition[];
  current_tier: MappingTier;
  content_edit_count: number;
};

export type ReviewQueueParams = {
  frameworkVersionId: string;
  tier?: MappingTier;
  conflictsOnly?: boolean;
  limit?: number;
  offset?: number;
};

export function reviewQueueQuery(params: ReviewQueueParams): string {
  const q = new URLSearchParams({
    framework_version_id: params.frameworkVersionId,
  });
  if (params.tier) q.set("tier", params.tier);
  if (params.conflictsOnly) q.set("conflicts_only", "true");
  if (params.limit !== undefined) q.set("limit", String(params.limit));
  if (params.offset !== undefined) q.set("offset", String(params.offset));
  return q.toString();
}

export async function fetchReviewQueue(
  params: ReviewQueueParams,
): Promise<CrosswalkReviewQueue> {
  return bffControlFetch<CrosswalkReviewQueue>(
    `/api/admin/crosswalk-review?${reviewQueueQuery(params)}`,
  );
}

export async function fetchEdgeAudit(
  edgeId: string,
): Promise<CrosswalkEdgeAudit> {
  return bffControlFetch<CrosswalkEdgeAudit>(
    `/api/admin/crosswalk-edges/${encodeURIComponent(edgeId)}/audit`,
  );
}

// mutate is the shared write helper for the two mutating calls below. It
// unwraps an upstream `{error}` body into the thrown APIError message so the
// form can render the backend's own wording ("the edit changes nothing",
// "illegal tier transition") rather than a bare status line — the backend is
// the authority on why a write was refused.
async function mutate<T>(
  path: string,
  method: "PATCH" | "POST",
  body: unknown,
): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON — keep the status line
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as T;
}

// editCrosswalkEdge changes a mapping's STRM CONTENT. It never approves
// anything: the backend writes an append-only audit row in the same
// transaction, and a verified mapping is DEMOTED to under_review by the edit
// rather than staying verified as something it no longer is.
export async function editCrosswalkEdge(
  edgeId: string,
  content: CrosswalkContent & { note?: string },
): Promise<CrosswalkEditResponse> {
  return mutate<CrosswalkEditResponse>(
    `/api/admin/crosswalk-edges/${encodeURIComponent(edgeId)}`,
    "PATCH",
    content,
  );
}

export type TierTransitionResponse = {
  edge_id: string;
  from_tier: MappingTier;
  to_tier: MappingTier;
  reviewer_id: string;
  note?: string;
  created_at: string;
};

// transitionCrosswalkTier is the approve/reject action. It posts to SLICE
// 483's endpoint — this slice deliberately ships no approval path of its own,
// so `verified` is reachable only through 483's state machine and only by a
// human clicking here. Nothing in the product auto-approves a mapping.
export async function transitionCrosswalkTier(
  edgeId: string,
  tier: MappingTier,
  note?: string,
): Promise<TierTransitionResponse> {
  return mutate<TierTransitionResponse>(
    `/api/admin/crosswalk-edges/${encodeURIComponent(edgeId)}/tier`,
    "POST",
    { tier, note },
  );
}

// --- conflict presentation helpers (pure) ---

// SEVERITY_ORDER ranks the slice-536a severities for the reviewer's queue.
// Detection is advisory (536a D5) — severity orders attention, it never blocks
// or auto-adjudicates anything.
const SEVERITY_ORDER: Record<ConflictSeverity, number> = {
  high: 0,
  medium: 1,
  low: 2,
};

// sortConflicts orders a requirement's findings high-severity first, then by
// reason so the ordering is total and stable. The backend already emits a
// deterministic order (536a D6); this is the reviewer-facing re-ordering on top
// of it, and being total is what keeps the list from reshuffling on re-render.
export function sortConflicts(
  conflicts: CrosswalkConflict[],
): CrosswalkConflict[] {
  return [...conflicts].sort(
    (a, b) =>
      SEVERITY_ORDER[a.severity] - SEVERITY_ORDER[b.severity] ||
      a.reason.localeCompare(b.reason),
  );
}

// conflictsForEdge selects the findings that name a specific edge. Several
// 536a heuristics are statements about a SET of edges, so one finding can
// attach to more than one row — and the orphaned-requirement family names no
// edge at all, which is why those stay visible at the requirement level and
// never disappear into a per-row view.
export function conflictsForEdge(
  conflicts: CrosswalkConflict[],
  edgeId: string,
): CrosswalkConflict[] {
  return sortConflicts(conflicts.filter((c) => c.edge_ids.includes(edgeId)));
}

// highestSeverity returns the most severe finding level present, or undefined
// for a clean requirement. Drives the requirement header badge.
export function highestSeverity(
  conflicts: CrosswalkConflict[],
): ConflictSeverity | undefined {
  return sortConflicts(conflicts)[0]?.severity;
}

// nextTiers returns the transitions slice 483's state machine permits from
// `from`. Mirrors internal/crosswalktier.legalTransitions — the UI hides
// illegal buttons for clarity, but the SERVER is the authority: an illegal
// move posted anyway is refused 422 there.
export function nextTiers(from: MappingTier): MappingTier[] {
  switch (from) {
    case "draft":
      return ["under_review", "rejected"];
    case "under_review":
      return ["verified", "rejected"];
    // Slice 536b D-536b-1 added the demotion edge; verified never goes
    // straight to draft or rejected.
    case "verified":
      return ["under_review"];
    case "rejected":
      return [];
  }
}
