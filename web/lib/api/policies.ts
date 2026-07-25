// Slice 370 — /policies list view (slice 101 + slice 107), extracted
// from the former `web/lib/api.ts` god-file.

import { APIError } from "./base";
import { apiFetch } from "./_shared";

// ----- Slice 101: /policies list view -----
//
// Row source: `policyWire` in `internal/api/policies/handlers.go` (the
// canonical mapping per design doc `Plans/canvas/12-ui-fill-in-design-
// decisions.md` §7). The page at `web/app/(authed)/policies/page.tsx`
// calls `fetchPoliciesList` from the browser; the BFF at
// `web/app/api/policies/route.ts` is the server-side counterpart that
// injects the bearer cookie (slice 094 pattern).
//
// Ack-rate sourcing: the slice 101 design doc + the slice text both
// note that `GET /v1/policies` should be extended with
// `?include=ack_rate` so the list endpoint returns one ack-rate cell
// per row in one round-trip. That extension does NOT exist on main as
// of slice 101 — the spillover slice 107 files it. Until it lands, the
// `ack_rate` field on `Policy` is `null`; the page renders an em-dash
// honestly (precedent: slice 098 D1 for state cells; same pattern).
// Client-side per-row fan-out is explicitly forbidden by P0-A2.

export type PolicyAckRate = {
  numerator: number;
  denominator: number;
  /** Percent in 0..100, null when denominator is zero or window unsettled. */
  percent: number | null;
};

export type Policy = {
  id: string;
  predecessor_id?: string | null;
  title: string;
  version: string;
  effective_date?: string | null;
  body_md: string;
  owner_role: string;
  approver_role: string;
  linked_control_ids: string[];
  acknowledgment_required_roles: string[];
  status: string;
  source_attribution: string;
  created_by: string;
  submitted_at?: string | null;
  submitted_by?: string | null;
  approved_at?: string | null;
  approved_by?: string | null;
  published_at?: string | null;
  published_by?: string | null;
  superseded_at?: string | null;
  created_at: string;
  updated_at: string;
  warnings?: string[];
  /**
   * Optional joined ack-rate cell. Set ONLY when the backend supports
   * `?include=ack_rate` (spillover slice 107). Until that lands the
   * field is always undefined / null and the page renders em-dash.
   */
  ack_rate?: PolicyAckRate | null;
};

export type PoliciesListResponse = {
  policies: Policy[];
};

// Server-side fetcher used by the slice 101 BFF route. Hits the
// upstream `/v1/policies?include=ack_rate` with the bearer.
//
// Slice 107: the `?include=ack_rate` query parameter is hard-coded on
// the BFF (mirrors slice 104's hard-coded `?include=state` for anchors)
// — every list-view caller wants the joined cell. The upstream returns
// `ack_rate: null` for non-published rows and a populated cell for
// published rows; the page renders accordingly.
export async function listPolicies(bearer: string): Promise<Policy[]> {
  const res = await apiFetch("/v1/policies?include=ack_rate", bearer);
  const body = (await res.json()) as { policies: Policy[]; count: number };
  return body.policies;
}

// Browser-side fetcher used by the slice 101 page. Hits the BFF at
// `/api/policies` which forwards the bearer cookie to upstream and
// hard-codes `?include=ack_rate` (slice 107).
export async function fetchPoliciesList(): Promise<PoliciesListResponse> {
  const res = await fetch(`/api/policies`);
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      /* body not JSON — keep the status line */
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as PoliciesListResponse;
}

// ----- Slice 672: /policies/[id] read-only detail view -----
//
// Server-side fetcher used by the slice 672 BFF `[id]` route. Hits the
// upstream `GET /v1/policies/{id}` with the bearer (RLS-tenant-scoped —
// the platform returns only the caller's-tenant policy). The wire shape
// is `{ policy: policyWire }` (see `wireFromPolicy` in
// `internal/api/policies/handlers.go`). `apiFetch` throws an `APIError`
// carrying the upstream status (404 / 401 / 5xx) which the BFF maps to
// a clean Next response. NO client-supplied tenant_id is accepted or
// forwarded (invariant #6 — identical posture to the list BFF).
export async function getPolicy(bearer: string, id: string): Promise<Policy> {
  const res = await apiFetch(`/v1/policies/${encodeURIComponent(id)}`, bearer);
  const body = (await res.json()) as { policy: Policy };
  return body.policy;
}

// Detail-page acknowledgment-rate cell. Shape mirrors the per-policy
// `GET /v1/policies/{id}/acknowledgment-rate` (`rateResponse` in
// `internal/api/policyacks/handlers.go`). `percent` is null when the
// denominator is zero (no required-role users).
export type PolicyDetailAckRate = {
  numerator: number;
  denominator: number;
  percent: number | null;
  window_seconds: number;
};

// Server-side ack-rate fetcher for the slice 672 detail BFF. Returns
// `null` (rather than throwing) on the documented non-200 cases:
//   - upstream 409: the policy is not published (no ack-rate to show)
//   - upstream 404: race against a deletion between the policy read and
//     this call — the page already has the policy, so degrade gracefully
//   - any other status: degrade gracefully — the ack-rate is a secondary
//     detail; its absence must not break the page render.
// This is ONE server-side call per detail page (not client per-row
// fan-out), so it respects the list view's P0-A2 discipline.
export async function getPolicyAckRate(
  bearer: string,
  id: string,
): Promise<PolicyDetailAckRate | null> {
  try {
    const res = await apiFetch(
      `/v1/policies/${encodeURIComponent(id)}/acknowledgment-rate`,
      bearer,
    );
    return (await res.json()) as PolicyDetailAckRate;
  } catch {
    return null;
  }
}

// Browser-side fetcher used by the slice 672 detail page. Hits the BFF
// at `/api/policies/{id}` which forwards the bearer cookie to upstream.
// On a 404 the BFF returns a 404 (the page maps that to `notFound()` so
// a genuinely-missing id renders the in-shell not-found boundary). The
// 401 path is left to the page's /login redirect (matches the vendors
// detail precedent). Other non-OK statuses surface the upstream `error`
// message verbatim (slice 367 — no internal-error leak: the BFF already
// scrubbed the upstream body to a clean `{error}` line).
export type PolicyDetailResponse = {
  policy: Policy;
  /** Null for non-published policies / zero-denominator / unavailable. */
  ack_rate: PolicyDetailAckRate | null;
};

export async function fetchPolicyDetail(
  id: string,
): Promise<PolicyDetailResponse> {
  const res = await fetch(`/api/policies/${encodeURIComponent(id)}`);
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      /* body not JSON — keep the status line */
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as PolicyDetailResponse;
}
