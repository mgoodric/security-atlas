// Slice 370 — risks list + create (slice 100 + slice 105), extracted
// from the former `web/lib/api.ts` god-file.

import { apiBaseURL, APIError } from "./base";
import { apiFetch } from "./_shared";
import { enumLabel } from "../labels/enum-labels";

// ----- Slice 100: /risks list view -----
//
// The slice-019 `riskWire` shape with the slice-067 hierarchy + severity
// fields. `inherent_score` and `residual_score` stay opaque JSON blobs
// (canvas §2.2: 5x5 grid is the v1 shape, but tenants may switch to FAIR
// later). The page renders them through pure formatters that defensively
// extract `{likelihood, impact}` — a malformed score formats as "—",
// matching the platform's degrade-quietly posture (`store.go`
// `residualMagnitude`).

export type Risk = {
  id: string;
  title: string;
  description: string;
  // #1812: the slug of the organisation category; category_name is its
  // display name.
  category: string;
  category_name?: string;
  methodology: string;
  inherent_score: unknown;
  treatment: string;
  // #1746: proposed | approved when treatment is accept; absent otherwise.
  acceptance_status?: "proposed" | "approved";
  treatment_owner: string;
  residual_score: unknown;
  review_due_at?: string;
  accepted_until?: string | null;
  accepter: string;
  instrument_reference: string;
  linked_control_ids: string[];
  created_at: string;
  updated_at: string;
  // Slice 067 additions — surfaced by the same handler.
  org_unit_id?: string;
  // #1749: the risk's hierarchy tier.
  level?: RiskLevel;
  themes: string[];
  severity: number;
  // #1748: the severity computed from the risk's children, beside its own
  // assessed `severity`. Only GET /v1/risks/{id} carries it, and only for a
  // risk with at least one scored child.
  rollup?: RiskRollUp;
  // #2060: how many child risks roll up into this one. GET /v1/risks sets
  // it on a roll-up, which the list only returns with include_rollups=true.
  child_count?: number;
  // #2105: the proposed or approved acceptance's record; present when
  // treatment is accept. The approved expiry and approver are
  // accepted_until and accepter above.
  acceptance?: RiskAcceptance;
  // #2107: open | closed. A closed risk carries its closure and is left
  // out of the register unless "Show closed" is on.
  status?: RiskStatus;
  closure?: RiskClosure;
};

export type RiskStatus = "open" | "closed";

export type RiskClosureReason =
  | "not_applicable"
  | "merged"
  | "realised_and_handled";

export type RiskClosure = {
  reason: RiskClosureReason;
  note?: string;
  closed_at?: string;
  closed_by: string;
};

export type RiskAcceptance = {
  justification: string;
  proposed_until?: string;
  proposed_by?: string;
  proposed_at?: string;
  approved_at?: string;
  self_approved: boolean;
};

export type RiskLevel = "team" | "org" | "company";

// ----- #1748: risk hierarchy links -----
//
// A parent risk's children roll up to it. Linking never changes the
// parent's stored score or treatment: the roll-up is computed from the
// children with the parent's severity function (max when it has none) and
// shown beside the assessed score.

export type RiskRollUp = {
  severity: number;
  severity_function: "max" | "weighted_max" | "sum";
  child_count: number;
  assessed_severity: number;
  // True when the children roll up higher than the parent was assessed.
  exceeds_assessed: boolean;
};

export type LinkedRisk = {
  id: string;
  title: string;
  level: RiskLevel;
  org_unit_id?: string;
  severity: number;
  // manual = attached by hand; rule = made by an aggregation rule.
  link_source: "manual" | "rule";
  rule_id?: string;
  linked_at: string;
};

export type RiskChildrenResponse = {
  children: LinkedRisk[];
  count: number;
  rollup?: RiskRollUp;
};

export type RiskParentsResponse = {
  parents: LinkedRisk[];
  count: number;
};

const LEVEL_RANK: Record<RiskLevel, number> = { team: 1, org: 2, company: 3 };

// canBeChildOf mirrors the platform's level rule: a child sits strictly
// below its parent (team < org < company). The attach picker offers only
// these; the platform re-checks, and also refuses cycles.
export function canBeChildOf(
  child: Pick<Risk, "level">,
  parent: Pick<Risk, "level">,
): boolean {
  const c = child.level ? LEVEL_RANK[child.level] : 0;
  const p = parent.level ? LEVEL_RANK[parent.level] : 0;
  return c > 0 && c < p;
}

async function hierarchyRequest<T>(
  url: string,
  init?: RequestInit,
): Promise<T | null> {
  const res = await fetch(url, init);
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
  if (res.status === 204) return null;
  return (await res.json()) as T;
}

export async function fetchRiskChildren(
  id: string,
): Promise<RiskChildrenResponse> {
  return (await hierarchyRequest<RiskChildrenResponse>(
    `/api/risks/${encodeURIComponent(id)}/children`,
  )) as RiskChildrenResponse;
}

export async function fetchRiskParents(
  id: string,
): Promise<RiskParentsResponse> {
  return (await hierarchyRequest<RiskParentsResponse>(
    `/api/risks/${encodeURIComponent(id)}/parents`,
  )) as RiskParentsResponse;
}

export async function attachChildRisks(
  id: string,
  childRiskIDs: string[],
): Promise<RiskChildrenResponse> {
  return (await hierarchyRequest<RiskChildrenResponse>(
    `/api/risks/${encodeURIComponent(id)}/children`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ child_risk_ids: childRiskIDs }),
    },
  )) as RiskChildrenResponse;
}

export async function detachChildRisk(
  id: string,
  childID: string,
): Promise<void> {
  await hierarchyRequest<null>(
    `/api/risks/${encodeURIComponent(id)}/children/${encodeURIComponent(
      childID,
    )}`,
    { method: "DELETE" },
  );
}

export type RisksListResponse = {
  risks: Risk[];
};

// Server-side fetcher used by the slice-100 BFF route. Mirrors the
// slice-098 `listAnchors` shape: no query-string narrowing — the page
// owns filter state client-side. The one exception is roll-ups (#2060):
// upstream leaves them out unless asked, so a parent never counts beside
// the children it summarises.
//
// #2107: closed risks are left out the same way unless includeClosed.
export async function listRisks(
  bearer: string,
  includeRollups = false,
  includeClosed = false,
): Promise<Risk[]> {
  const qs = new URLSearchParams();
  if (includeRollups) qs.set("include_rollups", "true");
  if (includeClosed) qs.set("include_closed", "true");
  const res = await apiFetch(
    qs.size ? `/v1/risks?${qs.toString()}` : "/v1/risks",
    bearer,
  );
  const body = (await res.json()) as { risks: Risk[]; count: number };
  return body.risks;
}

// Browser-side fetcher used by the slice-100 page. Hits the BFF at
// `/api/risks` which forwards the bearer cookie to upstream.
export async function fetchRisksList(): Promise<RisksListResponse> {
  return fetchRisks(`/api/risks`);
}

// fetchRisksListWithRollUps is fetchRisksList with roll-up parents kept
// (#2060): the register's "Show roll-ups" toggle, and the pickers where a
// roll-up is a valid choice.
export async function fetchRisksListWithRollUps(): Promise<RisksListResponse> {
  return fetchRisks(`/api/risks?include_rollups=true`);
}

// fetchRegisterRisks is the register's read: roll-ups and closed risks
// each kept only when its toggle is on (#2060, #2107).
export async function fetchRegisterRisks(opts: {
  rollups: boolean;
  closed: boolean;
}): Promise<RisksListResponse> {
  const qs = new URLSearchParams();
  if (opts.rollups) qs.set("include_rollups", "true");
  if (opts.closed) qs.set("include_closed", "true");
  return fetchRisks(qs.size ? `/api/risks?${qs.toString()}` : "/api/risks");
}

async function fetchRisks(url: string): Promise<RisksListResponse> {
  const res = await fetch(url);
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
  return (await res.json()) as RisksListResponse;
}

// ----- #1817: organisation risk categories -----
//
// The GET /v1/risks/categories wire shape (#1809, #1810). `slug` is the
// stable reference: the form posts it and the register filter matches it,
// so a renamed category never breaks a saved form or a bookmarked filter.
// `name` is what the user reads. An archived category stays on the risks
// that have it and is filterable, but takes no new risks.

export type RiskCategory = {
  id: string;
  slug: string;
  name: string;
  description: string;
  archived: boolean;
  is_default: boolean;
  risk_count: number;
  rule_count: number;
};

// Browser-side fetcher for the `/api/risks/categories` BFF: active
// categories only unless `includeArchived`.
export async function fetchRiskCategories(
  includeArchived = false,
): Promise<RiskCategory[]> {
  const res = await fetch(
    includeArchived
      ? "/api/risks/categories?include_archived=true"
      : "/api/risks/categories",
    { cache: "no-store" },
  );
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
  const body = (await res.json()) as { categories?: RiskCategory[] };
  return body.categories ?? [];
}

// ----- Slice 681: per-risk read-only detail -----
//
// The platform already serves `GET /v1/risks/{id}` (`GetRisk` in
// `internal/api/risks/handlers.go`), RLS-tenant-scoped via the store
// (`h.store.Get(ctx, id)` runs under the cookie session's tenant
// context — invariant #6). The BFF at `/api/risks/{id}` carries the
// bearer cookie and forwards it; no tenant_id is ever passed by the
// client. The detail page is READ-ONLY (slice 681 anti-criterion).
//
// The single-risk response carries the same `risk` wire shape as a list
// row plus, when the platform's residual deriver is wired, a `residual`
// breakdown block (slice 020). The breakdown is OPTIONAL — the page
// degrades to the row's stored `residual_score` when it is absent — so
// the detail page never breaks if the deriver is not configured.

export type RiskResidualBreakdown = {
  magnitude?: number | null;
  effectiveness?: number | null;
  // The platform's residualWireFrom shape is intentionally opaque here —
  // the page reads only the documented numeric fields and renders the
  // rest defensively. Kept loose so a deriver-shape change upstream does
  // not break the BFF type contract.
  [k: string]: unknown;
};

export type RiskDetailResponse = {
  risk: Risk;
  residual?: RiskResidualBreakdown | null;
};

// Server-side single-risk fetcher used by the slice-681 detail BFF.
// Mirrors `getPolicy` (slice 672): hit the platform with the bearer,
// decode `{ risk, residual? }`.
export async function getRisk(
  bearer: string,
  id: string,
): Promise<RiskDetailResponse> {
  const res = await apiFetch(`/v1/risks/${encodeURIComponent(id)}`, bearer);
  return (await res.json()) as RiskDetailResponse;
}

// Browser-side single-risk fetcher used by the slice-681 detail page.
// Hits the BFF at `/api/risks/{id}`. Mirrors `fetchPolicyDetail`.
export async function fetchRiskDetail(id: string): Promise<RiskDetailResponse> {
  const res = await fetch(`/api/risks/${encodeURIComponent(id)}`);
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
  return (await res.json()) as RiskDetailResponse;
}

// ----- Slice 105: risk-create wire shape -----
//
// `RiskCreateInput` mirrors `createReq` in
// `internal/api/risks/handlers.go` exactly. The form binds directly to
// this shape — no invented fields per P0-A4. `inherent_score` /
// `residual_score` stay opaque JSON blobs by design (canvas §2.2 — 5x5
// today, FAIR/dollar-banded tomorrow). The slice-105 form constructs
// `{likelihood, impact}` for the 5x5 case because that is what
// `severityOf()` reads downstream.
//
// Optional fields (review_due_at, accepted_until, accepter,
// instrument_reference, linked_control_ids, residual_score, description)
// are NOT enumerated in the slice-105 AC list — the form omits them
// rather than invent UI for them. The wire shape carries them for the
// future slice that adds the richer editor.

export type RiskCreateInput = {
  title: string;
  description?: string;
  category: string;
  methodology?: string;
  inherent_score?: unknown;
  treatment?: string;
  acceptance_status?: "proposed" | "approved";
  treatment_owner?: string;
  residual_score?: unknown;
  review_due_at?: string | null;
  accepted_until?: string | null;
  accepter?: string;
  instrument_reference?: string;
  linked_control_ids?: string[];
};

// ----- #2104: risk record editing + audit trail -----
//
// `RiskPatchInput` is the record-field half of `patchReq`
// (`internal/api/risks/handlers.go`). Treatment and acceptance are not
// on it: the platform refuses them on PATCH. An omitted field keeps its
// value, so the edit form sends only the fields the user changed.

export type RiskPatchInput = {
  title?: string;
  description?: string;
  category?: string;
  methodology?: string;
  inherent_score?: unknown;
  treatment_owner?: string;
  review_due_at?: string;
  instrument_reference?: string;
};

// One row of GET /v1/risks/{id}/audit-log, newest first. For
// fields_changed, before/after hold only the fields that changed.
export type RiskAuditEntry = {
  id: string;
  actor: string;
  action:
    | "fields_changed"
    | "category_changed"
    | "placement_changed"
    | "treatment_changed"
    | "acceptance_proposed"
    | "acceptance_approved"
    | "acceptance_rejected"
    | "acceptance_revoked"
    | string;
  before: Record<string, unknown>;
  after: Record<string, unknown>;
  created_at: string;
};

export type RiskAuditLogResponse = {
  entries: RiskAuditEntry[];
  count: number;
};

/** PATCH a risk's record fields through the BFF. */
export async function updateRisk(
  id: string,
  body: RiskPatchInput,
): Promise<Risk> {
  const decoded = (await hierarchyRequest<{ risk: Risk }>(
    `/api/risks/${encodeURIComponent(id)}`,
    {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    },
  )) as { risk: Risk };
  return decoded.risk;
}

// ----- #2105: treatment and acceptance transitions -----
//
// Treatment and acceptance change only through these, each its own
// platform route under /v1/risks/{id}; the BFF forwards them unchanged.
// The platform owns every refusal: 400 for input, 403 for the approver
// role or the approval rule, 409 for a transition the state forbids.

export type RiskTransition =
  | "treatment"
  | "acceptance/propose"
  | "acceptance/approve"
  | "acceptance/reject"
  | "acceptance/revoke"
  | "close"
  | "reopen";

export type RiskTransitionBody = {
  treatment?: string;
  instrument_reference?: string;
  justification?: string;
  proposed_until?: string;
  accepted_until?: string;
  reason?: string;
  // #2107: close takes a reason and an optional note.
  note?: string;
};

/** Run one treatment or acceptance transition through the BFF. */
export async function transitionRisk(
  id: string,
  transition: RiskTransition,
  body: RiskTransitionBody,
): Promise<Risk> {
  const decoded = (await hierarchyRequest<{ risk: Risk }>(
    `/api/risks/${encodeURIComponent(id)}/${transition}`,
    {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    },
  )) as { risk: Risk };
  return decoded.risk;
}

/** The risk's change history through the BFF. */
export async function fetchRiskAuditLog(
  id: string,
): Promise<RiskAuditLogResponse> {
  return (await hierarchyRequest<RiskAuditLogResponse>(
    `/api/risks/${encodeURIComponent(id)}/audit-log`,
  )) as RiskAuditLogResponse;
}

export type RiskCreatedResponse = {
  risk: Risk;
};

// Server-side fn: hit the platform directly with the bearer. Mirrors
// `createVendor`'s shape (slice 024). The form goes through the BFF at
// `POST /api/risks` instead so the bearer stays httpOnly.
export async function createRisk(
  bearer: string,
  body: RiskCreateInput,
): Promise<Risk> {
  const res = await fetch(`${apiBaseURL()}/v1/risks`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
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
  const decoded = (await res.json()) as RiskCreatedResponse;
  return decoded.risk;
}

// treatmentLabel renders a risk's treatment for display (#1746): an
// acceptance shows its status, so a pending one never reads as signed off.
// #2083: the treatment and status read as labels ("Accept (proposed)").
export function treatmentLabel(
  r: Pick<Risk, "treatment" | "acceptance_status">,
): string {
  const label = enumLabel("RiskTreatment", r.treatment);
  if (r.treatment === "accept" && r.acceptance_status) {
    const status = enumLabel("RiskAcceptanceStatus", r.acceptance_status);
    return `${label} (${status.toLowerCase()})`;
  }
  return label;
}
