// Slice 370 — control detail view (slice 041 + slice 253), extracted
// from the former `web/lib/api.ts` god-file. Binds the UCF-coverage,
// control-state, effectiveness, effective-scope, and per-control
// policies/risks/history reads.

import { apiFetch, bffControlFetch } from "./_shared";

// ===== Slice 041 — Control detail view =====
//
// Binds three already-merged backend slices into the /controls/[id] view:
//
//   * Slice 008 — UCF graph traversal
//       GET /v1/controls/{id}/coverage          control + anchor + requirements[]
//   * Slice 012 — control state evaluation
//       GET /v1/controls/{id}/state             per-scope-cell evaluated state
//       GET /v1/controls/{id}/effectiveness     rolling 30-day pass rate
//   * Slice 018 — FrameworkScope intersection
//       GET /v1/controls/{id}/effective-scope?framework_version=<UUID>
//
// The `relationship_type` (STRM) field is typed as an OPEN string, not a
// closed union. The DB enum has five values (equal, subset_of, superset_of,
// intersects_with, no_relationship — `internal/db/dbx/models.go`); the
// slice-005 `RequirementWithMapping.strm_type` 3-value union pre-dates the
// full enum and would silently drop `superset_of`. Rendering the raw string
// with a known-value style map + neutral fallback is drift-proof and honors
// the slice's anti-criterion against fabricated mappings.
//
// NOTE: there is no `GET /v1/evidence?control_id=...` list endpoint on main
// (only `POST /v1/evidence:push`). The evidence-stream section of the view
// renders an empty-state naming that gap; no evidence client fn exists here
// until that endpoint ships.

// controlWire mirrors `controlWire` in internal/api/ucfcoverage/handlers.go.
export type ControlWire = {
  id: string;
  bundle_id: string;
  version: number;
  scf_id?: string;
  scf_anchor_id?: string;
  title: string;
  control_family: string;
  implementation_type: string;
  lifecycle_state: string;
  owner_role: string;
  freshness_class?: string;
};

// anchorWire mirrors `anchorWire` in the same handler (bare anchor form).
export type ControlAnchorWire = {
  id: string;
  scf_id: string;
  family: string;
  name: string;
  description?: string;
};

// requirementForAnchorWire mirrors the same handler's per-requirement row.
// `relationship_type` is the STRM edge label — open string by design.
//
// Slice 256 — `coverage` is the per-row weighted score
// (strength × 30-day effectiveness, intersected with the framework's
// scope predicate). Always present on /v1/controls/{id}/coverage as a
// number-or-null (never undefined): null when the row's
// framework_version is out of scope OR when the control has no
// effectiveness data yet. The frontend MUST NOT compute coverage
// client-side as a fallback (slice 256 anti-criterion P0-256-1).
export type CoverageRequirement = {
  edge_id: string;
  requirement_id: string;
  code: string;
  title: string;
  body?: string;
  framework_slug: string;
  framework_name: string;
  framework_version: string;
  framework_version_id: string;
  framework_version_status: string;
  relationship_type: string;
  strength: number;
  coverage: number | null;
  source_attribution: string;
  rationale?: string;
};

export type ControlCoverage = {
  control: ControlWire;
  anchor: ControlAnchorWire | null;
  requirements: CoverageRequirement[];
};

// stateWire mirrors `stateWire` in internal/api/controlstate/handlers.go.
export type ControlStateEntry = {
  scope_cell_id: string | null;
  result: string;
  freshness_status: string;
  evidence_count_in_window: number;
  last_observed_at: string | null;
  evaluated_at: string;
  freshness_class: string;
  trigger: string;
};

export type ControlStateResponse = {
  control_id: string;
  states: ControlStateEntry[];
  count: number;
};

// effectivenessWire mirrors `effectivenessWire` in the controlstate handler.
export type ControlEffectiveness = {
  control_id: string;
  pass_rate: number;
  pass_count: number;
  total_count: number;
  window_start: string;
  window_end: string;
};

// EffectiveScope response from internal/api/frameworkscopes/handlers.go.
export type EffectiveScopeCell = {
  id: string;
  label: string;
  dimensions: Record<string, unknown>;
};

export type EffectiveScopeResponse = {
  control_id: string;
  framework_version_id: string;
  framework_scope_id: string | null;
  effective_scope: EffectiveScopeCell[];
  effective_scope_count: number;
  in_scope: boolean;
  out_of_scope_reason?: string;
};

// ===== Slice 253 — per-control policies / risks / history wire shapes =====
//
// Row sources are `policyWire`, `riskWire`, and `historyWire` in
// `internal/api/controldetail/handler.go` (slice 064). Endpoints have
// shipped on main since slice 064 + slice 106 (the evidence-list peer),
// but the control-detail view never re-pointed past slice 041's
// endpoint-pending placeholders — slice 253 wires the four reads.

// policyWire — one row of GET /v1/controls/{id}/policies.
export type ControlLinkedPolicy = {
  policy_id: string;
  title: string;
  version: string;
  status: string;
};

export type ControlLinkedPoliciesResponse = {
  control_id: string;
  policies: ControlLinkedPolicy[];
  count: number;
};

// riskWire — one row of GET /v1/controls/{id}/risks. The score fields
// stay opaque JSON blobs by design (canvas §2.2 — the 5x5 case carries
// `{likelihood, impact}` numerics; FAIR-shaped scores are valid too).
// The page extracts a display value via `formatResidualScore` from
// `app/(authed)/risks/filters.ts`.
export type ControlLinkedRisk = {
  risk_id: string;
  title: string;
  inherent_score: unknown;
  residual_score: unknown;
  link_weight: number | null;
};

export type ControlLinkedRisksResponse = {
  control_id: string;
  risks: ControlLinkedRisk[];
  count: number;
};

// historyWire — one row of GET /v1/controls/{id}/history. Newest-first,
// keyset-paginated; we render the most recent ~8 entries on the
// right-rail audit-log card (this view does NOT paginate — the dedicated
// History tab/page is the spillover for deeper trails).
export type ControlHistoryEntry = {
  evaluated_at: string;
  scope_cell: string | null;
  computed_state: string;
  freshness_status: string;
  evidence_count: number;
};

export type ControlHistoryResponse = {
  control_id: string;
  history: ControlHistoryEntry[];
  count: number;
  next_cursor: string;
};

// ----- server-side fns (called by the BFF route handlers) -----

export async function getControlCoverage(
  bearer: string,
  controlID: string,
): Promise<ControlCoverage> {
  const res = await apiFetch(
    `/v1/controls/${encodeURIComponent(controlID)}/coverage`,
    bearer,
  );
  return (await res.json()) as ControlCoverage;
}

export async function getControlState(
  bearer: string,
  controlID: string,
): Promise<ControlStateResponse> {
  const res = await apiFetch(
    `/v1/controls/${encodeURIComponent(controlID)}/state`,
    bearer,
  );
  return (await res.json()) as ControlStateResponse;
}

export async function getControlEffectiveness(
  bearer: string,
  controlID: string,
): Promise<ControlEffectiveness> {
  const res = await apiFetch(
    `/v1/controls/${encodeURIComponent(controlID)}/effectiveness`,
    bearer,
  );
  return (await res.json()) as ControlEffectiveness;
}

export async function getControlEffectiveScope(
  bearer: string,
  controlID: string,
  frameworkVersionID: string,
): Promise<EffectiveScopeResponse> {
  const res = await apiFetch(
    `/v1/controls/${encodeURIComponent(controlID)}/effective-scope` +
      `?framework_version=${encodeURIComponent(frameworkVersionID)}`,
    bearer,
  );
  return (await res.json()) as EffectiveScopeResponse;
}

// Slice 253 — per-control policies / risks / history (server-side).
//
// Each thin wrapper rides the same `apiFetch` helper used by the
// coverage / state / effectiveness / effective-scope reads above, which
// throws `APIError` on a non-2xx upstream response. The BFF route
// handlers in `app/api/controls/[id]/{policies,risks,history}/route.ts`
// catch the error and propagate the upstream status + message.

export async function getControlPolicies(
  bearer: string,
  controlID: string,
): Promise<ControlLinkedPoliciesResponse> {
  const res = await apiFetch(
    `/v1/controls/${encodeURIComponent(controlID)}/policies`,
    bearer,
  );
  return (await res.json()) as ControlLinkedPoliciesResponse;
}

export async function getControlRisks(
  bearer: string,
  controlID: string,
): Promise<ControlLinkedRisksResponse> {
  const res = await apiFetch(
    `/v1/controls/${encodeURIComponent(controlID)}/risks`,
    bearer,
  );
  return (await res.json()) as ControlLinkedRisksResponse;
}

// ===== Slice 444 — AI gap-explanation v0 =====
//
// GET /v1/controls/{id}/gap-explanation returns the DETERMINISTIC freshness
// rollup ALWAYS, plus a NON-BINDING, cited, local-Ollama explanation when one
// is available AND every citation resolves to a tenant-owned row. The
// explanation is null when suppressed (graceful degradation); the rollup is
// always present. The explanation is a comprehension aid — never an audit
// artifact, with no approve/publish/export affordance.

export type GapEvidenceFact = {
  evidence_id: string;
  evidence_kind: string;
  result: string;
  observed_at: string;
};

export type GapRollup = {
  control_id: string;
  control_title: string;
  freshness_class: string;
  is_stale: boolean;
  evidence_count: number;
  latest_observed_at: string | null;
  valid_until: string | null;
  evidence: GapEvidenceFact[];
};

export type GapCitation = {
  kind: "control" | "evidence";
  id: string;
};

export type GapExplanationBody = {
  text: string;
  citations: GapCitation[];
  // Human-friendly composed model string + the structured provenance fields
  // (slice-182 contract shape) so a future cloud-routing banner can read the
  // provider without re-parsing prose.
  model: string;
  model_name: string;
  model_version: string;
  model_provider: string;
  // ALWAYS false — the explanation is non-binding (no approve/publish/export).
  binding: boolean;
  // Human-readable "AI-generated explanation (model X) — not an audit
  // artifact" disclosure.
  disclosure: string;
};

export type ControlGapExplanationResponse = {
  control_id: string;
  rollup: GapRollup;
  // null when the explanation was suppressed (generation unavailable or a
  // citation failed to resolve) — the rollup still renders.
  explanation: GapExplanationBody | null;
  suppressed_reason: string;
};

// ===== Slice 502 — AI evidence-summarization v0 =====
//
// GET /v1/controls/{id}/evidence-summary returns the DETERMINISTIC bounded
// CURRENT LIVE evidence set ALWAYS (top-N most-recent records), plus a
// NON-BINDING, cited, local-default-Ollama summary of that evidence when one is
// available AND every citation resolves to a tenant-owned row. The summary is
// null when suppressed (graceful degradation); the evidence set is always
// present. The summary is a comprehension aid — never an audit artifact, with no
// approve/publish/export affordance. Sibling of slice 444's gap-explanation.

export type EvidenceSummaryFact = {
  evidence_id: string;
  evidence_kind: string;
  result: string;
  observed_at: string;
};

export type EvidenceSummarySet = {
  control_id: string;
  control_title: string;
  // The bound: `showing` records of `total` live records on record. The summary
  // is over the bounded `showing` set, never the full history (P0-502-8).
  showing: number;
  total: number;
  // ALWAYS true — current live evidence only, never a frozen audit-period
  // population (P0-502-5).
  live_only: boolean;
  records: EvidenceSummaryFact[];
};

export type EvidenceSummaryCitation = {
  kind: "control" | "evidence";
  id: string;
};

export type EvidenceSummaryBody = {
  text: string;
  citations: EvidenceSummaryCitation[];
  model: string;
  model_name: string;
  model_version: string;
  model_provider: string;
  // ALWAYS false — the summary is non-binding (no approve/publish/export).
  binding: boolean;
  // Human-readable "AI-generated summary (model X) — not an audit artifact"
  // disclosure.
  disclosure: string;
};

export type ControlEvidenceSummaryResponse = {
  control_id: string;
  evidence: EvidenceSummarySet;
  // null when the summary was suppressed (generation unavailable, no evidence,
  // or a citation failed to resolve) — the evidence set still renders.
  summary: EvidenceSummaryBody | null;
  suppressed_reason: string;
};

export async function getControlEvidenceSummary(
  bearer: string,
  controlID: string,
): Promise<ControlEvidenceSummaryResponse> {
  const res = await apiFetch(
    `/v1/controls/${encodeURIComponent(controlID)}/evidence-summary`,
    bearer,
  );
  return (await res.json()) as ControlEvidenceSummaryResponse;
}

export async function getControlHistory(
  bearer: string,
  controlID: string,
): Promise<ControlHistoryResponse> {
  // Right-rail audit-log card renders the latest few entries; the
  // upstream default page size is 50 and that's plenty for the
  // most-recent view. Deeper paginated history is the dedicated
  // History tab/page (slice 254 follow-on).
  const res = await apiFetch(
    `/v1/controls/${encodeURIComponent(controlID)}/history`,
    bearer,
  );
  return (await res.json()) as ControlHistoryResponse;
}

export async function getControlGapExplanation(
  bearer: string,
  controlID: string,
): Promise<ControlGapExplanationResponse> {
  const res = await apiFetch(
    `/v1/controls/${encodeURIComponent(controlID)}/gap-explanation`,
    bearer,
  );
  return (await res.json()) as ControlGapExplanationResponse;
}

// ----- browser-side fns (hit the BFF under /api/controls/**) -----

export function fetchControlCoverage(
  controlID: string,
): Promise<ControlCoverage> {
  return bffControlFetch<ControlCoverage>(
    `/api/controls/${encodeURIComponent(controlID)}/coverage`,
  );
}

export function fetchControlState(
  controlID: string,
): Promise<ControlStateResponse> {
  return bffControlFetch<ControlStateResponse>(
    `/api/controls/${encodeURIComponent(controlID)}/state`,
  );
}

export function fetchControlEffectiveness(
  controlID: string,
): Promise<ControlEffectiveness> {
  return bffControlFetch<ControlEffectiveness>(
    `/api/controls/${encodeURIComponent(controlID)}/effectiveness`,
  );
}

export function fetchControlEffectiveScope(
  controlID: string,
  frameworkVersionID: string,
): Promise<EffectiveScopeResponse> {
  return bffControlFetch<EffectiveScopeResponse>(
    `/api/controls/${encodeURIComponent(controlID)}/effective-scope` +
      `?framework_version=${encodeURIComponent(frameworkVersionID)}`,
  );
}

// Slice 253 — browser-side fetchers for the per-control policies /
// risks / history reads. Each rides the existing `bffControlFetch`
// helper so APIError surfaces with the upstream status; the
// control-detail page's `classifyControlDetailError` already routes
// 401 / 404 / other in a single discriminator and the new queries
// reuse it for free.

export function fetchControlPolicies(
  controlID: string,
): Promise<ControlLinkedPoliciesResponse> {
  return bffControlFetch<ControlLinkedPoliciesResponse>(
    `/api/controls/${encodeURIComponent(controlID)}/policies`,
  );
}

export function fetchControlRisks(
  controlID: string,
): Promise<ControlLinkedRisksResponse> {
  return bffControlFetch<ControlLinkedRisksResponse>(
    `/api/controls/${encodeURIComponent(controlID)}/risks`,
  );
}

export function fetchControlHistory(
  controlID: string,
): Promise<ControlHistoryResponse> {
  return bffControlFetch<ControlHistoryResponse>(
    `/api/controls/${encodeURIComponent(controlID)}/history`,
  );
}

// Slice 444 — browser-side fetcher for the gap-explanation read. Rides the
// existing bffControlFetch helper so APIError surfaces with the upstream
// status; the control-detail page's classifyControlDetailError routes 401 /
// 404 / other for free.
export function fetchControlGapExplanation(
  controlID: string,
): Promise<ControlGapExplanationResponse> {
  return bffControlFetch<ControlGapExplanationResponse>(
    `/api/controls/${encodeURIComponent(controlID)}/gap-explanation`,
  );
}

// Slice 502 — browser-side fetcher for the evidence-summary read. Rides the
// existing bffControlFetch helper so APIError surfaces with the upstream status;
// the control-detail page's classifyControlDetailError routes 401 / 404 / other
// for free.
export function fetchControlEvidenceSummary(
  controlID: string,
): Promise<ControlEvidenceSummaryResponse> {
  return bffControlFetch<ControlEvidenceSummaryResponse>(
    `/api/controls/${encodeURIComponent(controlID)}/evidence-summary`,
  );
}
