// Slice 370 — risks list + create (slice 100 + slice 105), extracted
// from the former `web/lib/api.ts` god-file.

import { apiBaseURL, APIError } from "./base";
import { apiFetch, upstreamErrorMessage } from "./_shared";
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
  // #2129: the owner, a user id, or null. owner_unlinked is the free-text
  // owner no user matched ("Owner (unlinked)"), "" whenever owner_user_id
  // is set. treatment_owner is the stored free text, kept for older
  // clients; read the owner from the two fields above.
  owner_user_id?: string | null;
  owner_unlinked?: string;
  // #2174: present and true when the owner can no longer work in Risk.
  owner_lacks_access?: boolean;
  treatment_owner: string;
  residual_score: unknown;
  review_due_at?: string;
  accepted_until?: string | null;
  accepter: string;
  instrument_reference: string;
  // #2838: the organisation's own risk ID ("R-014"), or null when unset.
  // Unique within the tenant.
  external_ref?: string | null;
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
    const msg = await upstreamErrorMessage(res);
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
  // #2132: the page's row count, the filtered total it pages over, and the
  // page the platform applied. Optional because hand-rolled fixtures and
  // the older callers predate them.
  count?: number;
  total?: number;
  limit?: number;
  offset?: number;
  // #2490: set by fetchAllRisks when it stopped at its page cap before
  // holding `total` rows; `risks` is then the first pages only.
  partial?: boolean;
};

// #2132: the GET /v1/risks parameters the BFF passes through. The
// platform checks every value and refuses one it does not allow.
export const RISK_LIST_PARAMS = [
  "status",
  "include_closed",
  "include_rollups",
  "owner",
  "treatment",
  "category",
  "methodology",
  "level",
  "org_unit",
  "theme",
  "severity_band",
  "review_due",
  "review_due_from",
  "review_due_to",
  "sort",
  "limit",
  "offset",
] as const;

export type RiskListParam = (typeof RISK_LIST_PARAMS)[number];

// Server-side fetcher used by the slice-100 BFF route. Each parameter on
// RISK_LIST_PARAMS is forwarded as given (#2132); with none the platform
// returns its default page: open risks, roll-ups left out (#2060, #2107),
// residual descending. A parameter given as a list is repeated
// (?category=a&category=b, #2482); the platform decides which keys take
// several values.
export async function listRisks(
  bearer: string,
  params: Partial<Record<RiskListParam, string | string[]>> = {},
): Promise<RisksListResponse> {
  const qs = new URLSearchParams();
  for (const key of RISK_LIST_PARAMS) {
    const raw = params[key];
    for (const v of Array.isArray(raw) ? raw : [raw]) {
      if (v) qs.append(key, v);
    }
  }
  const res = await apiFetch(
    qs.size ? `/v1/risks?${qs.toString()}` : "/v1/risks",
    bearer,
  );
  const body = (await res.json()) as RisksListResponse;
  return {
    risks: body.risks ?? [],
    count: body.count,
    total: body.total,
    limit: body.limit,
    offset: body.offset,
  };
}

// #2490: the register's size for its "N of M" label: the platform's
// filtered `total` for the default register (open, roll-ups left out), read
// off a one-row page. A response without `total` (an older platform, or a
// hand-rolled fixture) falls back to the rows it returned.
export async function fetchRiskRegisterTotal(): Promise<number> {
  const page = await fetchRisks("/api/risks?limit=1");
  return page.total ?? (page.risks ?? []).length;
}

// #2490: the sidebar badge's count: open, non-roll-up risks in the High
// severity band, counted by the platform (?severity_band=high, the bands of
// risk.SeverityBand) off a one-row page. A response without `total` is an
// error, so the badge renders nothing rather than a count of one page.
export async function fetchHighSeverityRiskTotal(): Promise<number> {
  const page = await fetchRisks("/api/risks?severity_band=high&limit=1");
  if (page.total === undefined) {
    throw new APIError(502, "risk list response has no total");
  }
  return page.total;
}

// fetchRisksListWithRollUps reads the whole register with roll-up parents
// kept (#2060): the pickers where a roll-up is a valid choice. It reads
// every page (#2482): GET /v1/risks returns at most 500 rows a call
// (#2132).
export async function fetchRisksListWithRollUps(): Promise<RisksListResponse> {
  return fetchAllRisks({ include_rollups: "true" });
}

// FETCH_ALL_RISKS_MAX_PAGES caps fetchAllRisks (#2490): 50 pages of 500 is
// 25,000 risks, far past a v1 register. It is insurance against an upstream
// whose `total` never stops growing or whose pages never fill.
export const FETCH_ALL_RISKS_MAX_PAGES = 50;

// fetchAllRisks walks the register a page at a time until it holds
// `total` rows. A page that comes back empty ends the walk, so a register
// that shrinks mid-walk cannot loop. At FETCH_ALL_RISKS_MAX_PAGES it stops
// and returns what it holds with `partial: true` rather than throwing, so a
// picker still offers the risks it read.
async function fetchAllRisks(
  query: Record<string, string>,
): Promise<RisksListResponse> {
  const risks: Risk[] = [];
  let total: number | undefined;
  for (let pages = 0; ; ) {
    const qs = new URLSearchParams(query);
    if (risks.length > 0) qs.set("offset", String(risks.length));
    const page = await fetchRisks(
      qs.size ? `/api/risks?${qs.toString()}` : "/api/risks",
    );
    pages++;
    const rows = page.risks ?? [];
    risks.push(...rows);
    total = page.total;
    if (rows.length === 0 || total === undefined || risks.length >= total) {
      break;
    }
    if (pages >= FETCH_ALL_RISKS_MAX_PAGES) {
      return { risks, count: risks.length, total, partial: true };
    }
  }
  return { risks, count: risks.length, total: total ?? risks.length };
}

// fetchRegisterPage is the register's read (#2132): one filtered, sorted
// page, with the filtered total. `query` is built by the page's
// registerQuery.
export async function fetchRegisterPage(
  query: Record<string, string>,
): Promise<RisksListResponse> {
  const qs = new URLSearchParams(query);
  return fetchRisks(qs.size ? `/api/risks?${qs.toString()}` : "/api/risks");
}

async function fetchRisks(url: string): Promise<RisksListResponse> {
  const res = await fetch(url);
  if (!res.ok) {
    const msg = await upstreamErrorMessage(res);
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
    const msg = await upstreamErrorMessage(res);
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
    const msg = await upstreamErrorMessage(res);
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
// Optional fields (review_due_at, instrument_reference,
// linked_control_ids, residual_score, description) are NOT enumerated in
// the slice-105 AC list — the form omits them rather than invent UI for
// them. The wire shape carries them for the future slice that adds the
// richer editor. accepted_until and accepter are left out on purpose:
// `createReq` still decodes them only to refuse them (#2371).

export type RiskCreateInput = {
  title: string;
  description?: string;
  category: string;
  methodology?: string;
  inherent_score?: unknown;
  treatment?: string;
  // #2371: create may only propose an acceptance. The API refuses
  // approved, accepter and accepted_until at create: the approval records
  // them.
  acceptance_status?: "proposed";
  // #2391: required with acceptance_status=proposed, as on
  // /acceptance/propose. proposed_until is YYYY-MM-DD, after today.
  justification?: string;
  proposed_until?: string;
  treatment_owner?: string;
  owner_user_id?: string;
  residual_score?: unknown;
  review_due_at?: string | null;
  instrument_reference?: string;
  linked_control_ids?: string[];
  // #2838: the organisation's own risk ID, unique within the tenant (409).
  external_ref?: string;
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
  // #2129: a user id sets the owner; null clears it.
  owner_user_id?: string | null;
  review_due_at?: string;
  instrument_reference?: string;
  // #2838: a string sets the organisation risk ID; null clears it.
  external_ref?: string | null;
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
    | "closed"
    | "reopened"
    | "control_linked"
    | "control_unlinked"
    | "created"
    | "themes_changed"
    | "child_linked"
    | "child_unlinked"
    | string;
  before: Record<string, unknown>;
  after: Record<string, unknown>;
  created_at: string;
};

export type RiskAuditLogResponse = {
  entries: RiskAuditEntry[];
  count: number;
  // #2311: the platform returns at most `limit` rows, newest first;
  // `truncated` is true when older changes exist past them.
  limit?: number;
  truncated?: boolean;
};

/**
 * The If-Match value for a risk as it was read (#2311): its updated_at,
 * quoted. The platform refuses (412) a PATCH carrying it once the risk has
 * changed since.
 */
export function riskIfMatch(risk: Pick<Risk, "updated_at">): string {
  return `"${risk.updated_at}"`;
}

/**
 * PATCH a risk's record fields through the BFF. With `ifMatch` (see
 * riskIfMatch) the edit applies only to the version it was made from.
 */
export async function updateRisk(
  id: string,
  body: RiskPatchInput,
  opts?: { ifMatch?: string },
): Promise<Risk> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };
  if (opts?.ifMatch) headers["If-Match"] = opts.ifMatch;
  const decoded = (await hierarchyRequest<{ risk: Risk }>(
    `/api/risks/${encodeURIComponent(id)}`,
    {
      method: "PATCH",
      headers,
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

/** #2106: link a control to the risk through the BFF. */
export async function linkRiskControl(
  id: string,
  controlId: string,
): Promise<Risk> {
  const decoded = (await hierarchyRequest<{ risk: Risk }>(
    `/api/risks/${encodeURIComponent(id)}/controls`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ control_id: controlId }),
    },
  )) as { risk: Risk };
  return decoded.risk;
}

/**
 * #2106: unlink a control from the risk through the BFF. The platform
 * refuses the last control of a mitigate risk with no live action plan.
 */
export async function unlinkRiskControl(
  id: string,
  controlId: string,
): Promise<Risk> {
  const decoded = (await hierarchyRequest<{ risk: Risk }>(
    `/api/risks/${encodeURIComponent(id)}/controls/${encodeURIComponent(
      controlId,
    )}`,
    { method: "DELETE" },
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
    const msg = await upstreamErrorMessage(res);
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
