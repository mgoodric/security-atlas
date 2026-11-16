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

import { APIError } from "./base";
import { bffControlFetch } from "./_shared";

export const REMAP_CLASSES = [
  "unchanged",
  "renamed",
  "split",
  "reused_id",
  "no_successor",
] as const;
export type RemapClass = (typeof REMAP_CLASSES)[number];

export type RemapStatus =
  | "open"
  | "rebound"
  | "approved"
  | "rejected"
  | "kept_legacy";

export interface RemapProposal {
  id: string;
  control_id: string;
  bundle_id: string;
  title: string;
  legacy_scf_id: string;
  legacy_release: string;
  target_release: string;
  class: RemapClass;
  status: RemapStatus;
  successor_scf_ids: string[];
  created_at: string;
}

export interface RemapProposalsResponse {
  proposals: RemapProposal[];
  count: number;
}

export interface RemapDecision {
  proposal_id: string;
  decision: string;
  new_scf_id?: string;
  new_release?: string;
  decided_by: string;
  decided_at: string;
}

export interface RemapBulkResult {
  approved_count: number;
  approved: RemapDecision[];
  skipped: { proposal_id: string; bundle_id: string; reason: string }[];
}

// An SCF anchor as GET /v1/anchors returns it. Titles are SCF's verbatim
// text and descriptions our own words; the page renders both as text.
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

export interface EdgeRemapProposal {
  id: string;
  edge_id: string;
  framework_slug: string;
  framework_version: string;
  requirement_code: string;
  relationship_type: string;
  legacy_scf_id: string;
  legacy_release: string;
  target_release: string;
  class: RemapClass;
  status: RemapStatus;
  successor_scf_ids: string[];
}

export interface EdgeRemapProposalsResponse {
  proposals: EdgeRemapProposal[];
}

export function fetchRemapProposals(): Promise<RemapProposalsResponse> {
  return bffControlFetch<RemapProposalsResponse>(
    "/api/controls/remap-proposals",
  );
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

async function postJSON<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
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
  return postJSON<RemapDecision>(
    `/api/controls/remap-proposals/${encodeURIComponent(proposalId)}/${action}`,
    body,
  );
}

/** bulkApproveUnchanged calls the platform's bulk endpoint once. */
export function bulkApproveUnchanged(): Promise<RemapBulkResult> {
  return postJSON<RemapBulkResult>(
    "/api/controls/remap-proposals/approve-unchanged",
  );
}
