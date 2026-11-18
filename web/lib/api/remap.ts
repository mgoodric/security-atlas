// Issue #1778 — browser-side client for the SCF remap review page.
//
// Every call goes through a BFF route under /api, which forwards the
// caller's own bearer cookie (never a service credential):
//
//   GET  /api/controls/remap-proposals              -> GET /v1/controls/remap-proposals
//   POST /api/controls/remap-proposals/approve-unchanged
//   POST /api/controls/remap-proposals/{id}/{approve|reject|keep-legacy}
//   GET  /api/controls/remap-proposals/anchors      -> GET /v1/anchors (global catalog, read-only)
//   GET  /api/admin/crosswalk-edge-remap-proposals  -> super-admin only upstream
//
// The platform is the gate on every one: control remap decisions are
// admin / grc_engineer, edge proposals are super-admin (a tenant admin
// gets 403, which the page reads as "hide the edge section").

import { bffControlFetch, bffPostJSON } from "./_shared";
import type { components } from "./generated/openapi";

// The platform's response bodies are generated from docs/openapi.yaml
// (#2394), whose schemas internal/api/openapi reflects from the Go wire
// structs. A renamed or retyped field fails typecheck here and in every
// caller instead of at runtime. Regenerate with `npm run openapi:types`.
type Schemas = components["schemas"];

export type RemapProposal = Schemas["RemapProposal"];
export type RemapProposalsResponse = Schemas["RemapProposalsResponse"];
export type RemapDecision = Schemas["RemapDecision"];
export type RemapBulkResult = Schemas["RemapBulkResult"];
export type EdgeRemapProposal = Schemas["EdgeRemapProposal"];
export type EdgeRemapProposalsResponse = Schemas["EdgeRemapProposalsResponse"];

export type RemapClass = RemapProposal["class"];
export type RemapStatus = RemapProposal["status"];

// The tab order: one entry per class, keyed so a class the platform adds
// or drops fails typecheck here.
const REMAP_CLASS_ORDER: Record<RemapClass, number> = {
  unchanged: 0,
  renamed: 1,
  split: 2,
  reused_id: 3,
  no_successor: 4,
};
export const REMAP_CLASSES: readonly RemapClass[] = (
  Object.keys(REMAP_CLASS_ORDER) as RemapClass[]
).sort((a, b) => REMAP_CLASS_ORDER[a] - REMAP_CLASS_ORDER[b]);

// The anchor types below are the BFF's own response, composed in
// app/api/controls/remap-proposals/anchors/route.ts, so they stay
// hand-written. An SCF anchor as GET /v1/anchors returns it. Titles are
// SCF's verbatim text and descriptions our own words; the page renders
// both as text.
export interface SCFAnchor {
  id: string;
  scf_id: string;
  family: string;
  name: string;
  description: string;
  legacy_scf_id?: string;
}

export interface AnchorLookupResponse {
  scf_release: string;
  // Keyed by code; null when the release has no anchor with that code.
  anchors: Record<string, SCFAnchor | null>;
}

export interface AnchorSearchResponse {
  scf_release: string;
  anchors: SCFAnchor[];
  truncated: boolean;
}

// The platform pages the list (#2325). The review page groups every
// pending proposal by class, so this reads every page, at the largest page
// size the platform allows.
export const REMAP_PAGE_LIMIT = 500;

export async function fetchRemapProposals(): Promise<RemapProposalsResponse> {
  const proposals: RemapProposal[] = [];
  let cursor: string | undefined;
  let total = 0;
  do {
    const qs = new URLSearchParams({ limit: String(REMAP_PAGE_LIMIT) });
    if (cursor) qs.set("cursor", cursor);
    const page = await bffControlFetch<RemapProposalsResponse>(
      `/api/controls/remap-proposals?${qs.toString()}`,
    );
    proposals.push(...page.proposals);
    total = page.total;
    cursor = page.next_cursor;
  } while (cursor);
  return { proposals, count: proposals.length, total };
}

export function fetchEdgeRemapProposals(): Promise<EdgeRemapProposalsResponse> {
  return bffControlFetch<EdgeRemapProposalsResponse>(
    "/api/admin/crosswalk-edge-remap-proposals",
  );
}

export function lookupAnchors(
  release: string,
  codes: string[],
): Promise<AnchorLookupResponse> {
  const qs = new URLSearchParams({ release, codes: codes.join(",") });
  return bffControlFetch<AnchorLookupResponse>(
    `/api/controls/remap-proposals/anchors?${qs.toString()}`,
  );
}

export function searchAnchors(
  release: string,
  q: string,
): Promise<AnchorSearchResponse> {
  const qs = new URLSearchParams({ release, q });
  return bffControlFetch<AnchorSearchResponse>(
    `/api/controls/remap-proposals/anchors?${qs.toString()}`,
  );
}

export type RemapAction = "approve" | "reject" | "keep-legacy";

/**
 * decideRemap sends one reviewer decision. `scfId` is the chosen anchor
 * in the target release; the page always names it for split, reused_id
 * and no_successor so nothing is approved by default.
 */
export function decideRemap(
  proposalId: string,
  action: RemapAction,
  body: { scf_id?: string; reason?: string } = {},
): Promise<RemapDecision> {
  return bffPostJSON<RemapDecision>(
    `/api/controls/remap-proposals/${encodeURIComponent(proposalId)}/${action}`,
    body,
  );
}

/** bulkApproveUnchanged calls the platform's bulk endpoint once. */
export function bulkApproveUnchanged(): Promise<RemapBulkResult> {
  return bffPostJSON<RemapBulkResult>(
    "/api/controls/remap-proposals/approve-unchanged",
  );
}
