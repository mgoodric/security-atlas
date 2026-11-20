// Slice 370 — /policies list view (slice 101 + slice 107), extracted
// from the former `web/lib/api.ts` god-file.

import { APIError } from "./base";
import {
  apiFetch,
  bffControlFetch,
  bffPostJSON,
  upstreamErrorMessage,
} from "./_shared";

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
  /**
   * #2101 — true when the submitter approved the policy under the
   * organisation's Allow self-approval setting.
   */
  self_approved?: boolean;
  published_at?: string | null;
  published_by?: string | null;
  superseded_at?: string | null;
  next_review_at?: string | null;
  /**
   * #1806 — where a published policy stands against `next_review_at`,
   * computed by the API at response time: `overdue` once the due day (the
   * UTC date of `next_review_at`) has ended, `due-soon` within 30 days of
   * it, else `upcoming`. Absent when the policy has no review date or is
   * not published; a superseded version keeps the date it was due (#2009).
   */
  review_status?: "overdue" | "due-soon" | "upcoming" | null;
  /**
   * #1747 — true when the policy was brought in by the policy library
   * import with its historical lifecycle. The historical approver is a
   * free-text name (not a credential); `source_url` is an absolute
   * http(s) URL validated on import.
   */
  imported?: boolean;
  historical_approver_name?: string | null;
  /** YYYY-MM-DD */
  historical_approved_at?: string | null;
  source_url?: string | null;
  imported_by?: string | null;
  imported_at?: string | null;
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
    const msg = await upstreamErrorMessage(res);
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
    const msg = await upstreamErrorMessage(res);
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as PolicyDetailResponse;
}

// ----- #2116: create, edit and the workflow from the app -----
//
// Browser-side calls to the policy BFF. The platform enforces every rule
// (the draft-only edit and discard, the approval rule, the publish block
// on a policy with no linked control); these only carry the request and
// surface the platform's message.

/** The fields a draft's author sets; version is fixed at create. */
export type PolicyDraftInput = {
  title: string;
  body_md: string;
  owner_role: string;
  approver_role: string;
  linked_control_ids: string[];
  acknowledgment_required_roles: string[];
};

export type CreatePolicyInput = PolicyDraftInput & {
  version: string;
  /** Set by "Create new version": the published policy this revises. */
  predecessor_id?: string;
};

export type PublishPolicyInput = {
  new_version: string;
  /** YYYY-MM-DD; omitted publishes effective today. */
  effective_date?: string;
  /** YYYY-MM-DD; omitted publishes with no review date. */
  next_review_at?: string;
};

async function parsePolicy(res: Response): Promise<Policy> {
  if (!res.ok) {
    const msg = await upstreamErrorMessage(res);
    throw new APIError(res.status, msg);
  }
  return ((await res.json()) as { policy: Policy }).policy;
}

function jsonInit(method: string, body?: unknown): RequestInit {
  return body === undefined
    ? { method }
    : {
        method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      };
}

export async function createPolicy(input: CreatePolicyInput): Promise<Policy> {
  return parsePolicy(await fetch("/api/policies", jsonInit("POST", input)));
}

export async function updatePolicyDraft(
  id: string,
  input: PolicyDraftInput,
): Promise<Policy> {
  return parsePolicy(
    await fetch(
      `/api/policies/${encodeURIComponent(id)}`,
      jsonInit("PATCH", input),
    ),
  );
}

export async function discardPolicyDraft(id: string): Promise<void> {
  const res = await fetch(`/api/policies/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
  if (!res.ok) await parsePolicy(res);
}

export type PolicyTransition = "submit" | "approve" | "publish";

export async function transitionPolicy(
  id: string,
  action: PolicyTransition,
  body?: PublishPolicyInput,
): Promise<Policy> {
  return parsePolicy(
    await fetch(
      `/api/policies/${encodeURIComponent(id)}/${action}`,
      jsonInit("POST", body),
    ),
  );
}

// ----- #2465: acknowledging a policy -----
//
// Wire shapes: `pendingResponse` and `ackResponse` in
// `internal/api/policyacks/handlers.go`. The pending list holds the
// published policies whose audience includes the caller and that the
// caller has not acknowledged for this version within the year.

export type PendingAcknowledgment = {
  policy_id: string;
  policy_version_id: string;
  title: string;
  version: string;
  effective_date?: string | null;
  required_roles: string[];
  /** An earlier acknowledgment: of an older version, or over a year ago. */
  last_acknowledged_at?: string | null;
  /** #2736 — `overdue` once a lapsed ack lapsed, or once the grace period
   *  after the effective date has ended for one never made; `due_soon`
   *  before then. Needs-attention bands the item the same way. */
  status?: "overdue" | "due_soon";
  /** #2736 — when the acknowledgement is (or was) due. */
  due_at?: string | null;
};

/** #2668 — a policy the caller has acknowledged, for its current version,
 *  within the year (`currentItem` in the Go handler). */
export type CurrentAcknowledgment = {
  policy_id: string;
  policy_version_id: string;
  title: string;
  version: string;
  acknowledged_at: string;
};

export type MyAcknowledgmentsResponse = {
  pending: PendingAcknowledgment[];
  count: number;
  window_seconds: number;
  /** Present because the fetch asks for `?include=current` (#2668). */
  current?: CurrentAcknowledgment[];
};

export type PolicyAcknowledgment = {
  id: string;
  policy_id: string;
  policy_version_id: string;
  acknowledged_at: string;
  deduplicated: boolean;
  /** False when the ack was recorded but its evidence record was not. */
  evidence_emission_ok: boolean;
};

/** The one query key for the caller's pending list, shared by the policy
 *  list's callout and the detail page so an ack refreshes both. */
export const MY_ACKS_QUERY_KEY = ["me", "acknowledgments"] as const;

export async function fetchMyAcknowledgments(): Promise<MyAcknowledgmentsResponse> {
  return bffControlFetch<MyAcknowledgmentsResponse>(
    "/api/me/acknowledgments?include=current",
  );
}

// The platform records the ack for the caller only. The body pins the
// version the caller was shown (the pending item's `policy_version_id`):
// if the policy was re-published since, the platform answers 409 rather
// than record an ack of a version they never saw (#2668).
export async function acknowledgePolicy(
  id: string,
  policyVersionId: string,
): Promise<PolicyAcknowledgment> {
  return bffPostJSON<PolicyAcknowledgment>(
    `/api/policies/${encodeURIComponent(id)}/acknowledge`,
    { policy_version_id: policyVersionId },
  );
}
