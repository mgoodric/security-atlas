// Slice 370 — hierarchical risk dashboard view (slice 056), extracted
// from the former `web/lib/api.ts` god-file.

import { apiFetch, bffControlFetch } from "./_shared";

// ===== Slice 056 — Hierarchical risk dashboard view =====
//
// The `/risks/hierarchy` view binds three panels — org tree, theme
// heatmap, decision timeline — each to a real backend endpoint via a
// thin BFF proxy under `/api/risks-hierarchy/**`. It extends the slice
// 040 pattern (`dashboardProxy`, `PanelCard`/`MissingEndpointPanel`,
// per-panel TanStack Query). No panel fabricates data (anti-criterion
// P0-1).
//
// Endpoint inventory verified against main `internal/api/` at slice
// time:
//
//   * Org tree structure   GET /v1/org_units            (slice 053) — bound
//       The AC asks for `?include_risk_counts=true`. The ListOrgUnits
//       handler ignores all query params and the slice-019 `riskWire`
//       predates slice 052 — there is no `org_unit_id`, `themes`, or a
//       severity field on a risk-list row, so per-node risk counts
//       cannot be derived client-side either. The tree STRUCTURE binds
//       and renders honestly; per-node count chips show a labelled
//       "pending endpoint" affordance naming `?include_risk_counts=true`
//       rather than fabricating zeros. AC-2 is PARTIAL.
//   * Theme vocabulary     GET /v1/themes               (slice 053) — bound
//       Real heatmap columns (10 default + tenant-private). The
//       `themes × org_units` cell-aggregation endpoint does NOT exist on
//       main — the heatmap renders its real axes and overlays a
//       `MissingEndpointPanel` for cell counts. AC-3/4/5 PARTIAL.
//   * Aggregation rules    GET /v1/aggregation-rules    (slice 054) — bound
//       Real `window_days` / `min_risks` / `min_teams` / `target_theme`
//       metadata so the heatmap cell-hover tooltip ("nearest rule fires
//       at {threshold}; window {window_days}d") cites real numbers, not
//       fabricated thresholds.
//   * Decisions            GET /v1/decisions            (slice 055) — bound
//   * Overdue decisions    GET /v1/decisions/overdue    (slice 055) — bound
//       The decision timeline panel is FULLY satisfiable — slice 055 is
//       merged. AC-6 / AC-7 are PASS.
//
// See `docs/audit-log/056-hierarchical-risk-dashboard-decisions.md` for
// the full missing-endpoint gap inventory so a follow-up backend slice
// can be scoped.

// OrgUnit mirrors `wire` in internal/api/orgunits/handlers.go.
export type OrgUnit = {
  id: string;
  name: string;
  parent_id?: string | null;
  level: string;
  acceptance_authorities: unknown;
};

export type OrgUnitListResponse = { org_units: OrgUnit[]; count: number };

// RiskTheme mirrors `themeWire` in internal/api/themes/handlers.go.
export type RiskTheme = {
  name: string;
  description: string;
  source: "default" | "tenant";
};

export type RiskThemeListResponse = { themes: RiskTheme[]; count: number };

// AggregationRule mirrors `ruleWire` in
// internal/api/aggregationrules/handler.go. Only the fields the heatmap
// tooltip cites are typed strictly; the rest are carried as-is.
export type AggregationRule = {
  id: string;
  rule_id: string;
  target_theme: string;
  min_risks: number;
  min_teams: number;
  window_days: number;
  parent_level: string;
  severity_function: string;
  title_template: string;
  status: string;
  activated_by?: string;
  activated_at?: string;
  created_at: string;
  updated_at: string;
};

export type AggregationRuleListResponse = {
  rules: AggregationRule[];
  count: number;
};

// Decision mirrors `decisionWire` in internal/api/decisions/handlers.go.
export type Decision = {
  id: string;
  decision_id: string;
  title: string;
  narrative: string;
  constraints: string[];
  tradeoffs: string;
  decision_maker: string;
  decided_at: string;
  revisit_by?: string;
  status: string;
  superseded_by?: string;
  audit_narrative_opt_out: boolean;
  created_at: string;
  updated_at: string;
};

export type DecisionListResponse = { decisions: Decision[]; count: number };

// DecisionFilter is the client-side filter state for the timeline panel.
// `status` filters by a single status string upstream; `constraints` and
// `decision_maker` are applied client-side over the returned rows (the
// ListDecisions handler exposes only `?status=` and
// `?revisit_due_within_days=`).
export type DecisionFilter = {
  status?: string;
};

// ----- server-side fns (called by the BFF route handlers) -----

export async function getOrgUnits(bearer: string): Promise<OrgUnit[]> {
  const res = await apiFetch(`/v1/org_units`, bearer);
  const body = (await res.json()) as OrgUnitListResponse;
  return body.org_units;
}

export async function getRiskThemes(bearer: string): Promise<RiskTheme[]> {
  const res = await apiFetch(`/v1/themes`, bearer);
  const body = (await res.json()) as RiskThemeListResponse;
  return body.themes;
}

export async function getAggregationRules(
  bearer: string,
): Promise<AggregationRule[]> {
  const res = await apiFetch(`/v1/aggregation-rules`, bearer);
  const body = (await res.json()) as AggregationRuleListResponse;
  return body.rules ?? [];
}

export async function getDecisions(
  bearer: string,
  status?: string,
): Promise<Decision[]> {
  const suffix = status ? `?status=${encodeURIComponent(status)}` : "";
  const res = await apiFetch(`/v1/decisions${suffix}`, bearer);
  const body = (await res.json()) as DecisionListResponse;
  return body.decisions;
}

export async function getOverdueDecisions(bearer: string): Promise<Decision[]> {
  const res = await apiFetch(`/v1/decisions/overdue`, bearer);
  const body = (await res.json()) as DecisionListResponse;
  return body.decisions;
}

// ----- browser-side fns (hit the BFF under /api/risks-hierarchy/**) -----

export function fetchHierarchyOrgUnits(): Promise<OrgUnit[]> {
  return bffControlFetch<OrgUnitListResponse>(
    `/api/risks-hierarchy/org-units`,
  ).then((b) => b.org_units);
}

export function fetchHierarchyThemes(): Promise<RiskTheme[]> {
  return bffControlFetch<RiskThemeListResponse>(
    `/api/risks-hierarchy/themes`,
  ).then((b) => b.themes);
}

export function fetchHierarchyAggregationRules(): Promise<AggregationRule[]> {
  return bffControlFetch<AggregationRuleListResponse>(
    `/api/risks-hierarchy/aggregation-rules`,
  ).then((b) => b.rules ?? []);
}

export function fetchHierarchyDecisions(status?: string): Promise<Decision[]> {
  const suffix = status ? `?status=${encodeURIComponent(status)}` : "";
  return bffControlFetch<DecisionListResponse>(
    `/api/risks-hierarchy/decisions${suffix}`,
  ).then((b) => b.decisions);
}

export function fetchHierarchyOverdueDecisions(): Promise<Decision[]> {
  return bffControlFetch<DecisionListResponse>(
    `/api/risks-hierarchy/decisions-overdue`,
  ).then((b) => b.decisions);
}
