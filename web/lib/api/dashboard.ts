// Slice 370 — program dashboard view (slice 040 + slice 147 re-point),
// extracted from the former `web/lib/api.ts` god-file.

import { apiFetch, bffControlFetch } from "./_shared";

// ===== Slice 040 — Program dashboard view (+ slice 147 re-point) =====
//
// The dashboard at `/dashboard` is the solo-security-leader persona's
// morning home screen. It binds six panels, each to a real backend
// endpoint via a thin BFF proxy under `/api/dashboard/**`. No panel
// fabricates data (anti-criterion P0-1).
//
// Endpoint inventory verified against main `internal/api/` (updated by
// slice 147 — slice 066 backend endpoints are now bound):
//
//   * Recent drift       GET /v1/controls/drift?since=7d    (slice 016) — bound
//   * Evidence freshness GET /v1/evidence/freshness         (slice 016) — bound
//   * Top risks aging    GET /v1/risks?treatment=mitigate   (slice 019) — bound
//       Server-side `sort=residual,age` (slice 066) is not yet passed —
//       spillover at slice 148.
//   * Upcoming items     GET /v1/exceptions/expiring?within=30d (slice 028) — bound
//       The unified rollup `/v1/upcoming` (slice 066) is not yet consumed —
//       spillover at slice 148.
//   * Framework posture  GET /v1/frameworks/posture          (slice 066) — bound by slice 147
//   * Activity feed      GET /v1/activity                    (slice 066) — bound by slice 147
//
// See `docs/audit-log/040-program-dashboard-view-decisions.md` and
// `docs/audit-log/147-dashboard-placeholders-decisions.md` for the full
// rebinding history.

// driftRowWire mirrors `driftRowWire` in internal/api/freshnessdrift/handlers.go.
export type DriftRow = {
  control_id: string;
  last_passing: string;
  current_result: string;
};

// DriftReport mirrors the Drift handler's JSON envelope.
export type DriftReport = {
  since: string;
  through: string;
  delta: number;
  flipped_out_count: number;
  flipped_out: DriftRow[];
};

// freshnessClassBucket mirrors `freshnessClassBucket` in the same handler.
export type FreshnessBucket = {
  freshness_class: string;
  total: number;
  fresh: number;
  stale: number;
};

// FreshnessReport mirrors the Freshness handler's JSON envelope.
export type FreshnessReport = {
  bucket: string;
  buckets: FreshnessBucket[];
  total: number;
  total_stale: number;
};

// DashboardRisk mirrors `riskWire` in internal/api/risks/handlers.go.
// `inherent_score` / `residual_score` are opaque JSON blobs by design —
// the dashboard renders them as-is and never parses an ordering out.
export type DashboardRisk = {
  id: string;
  title: string;
  description: string;
  category: string;
  methodology: string;
  inherent_score: unknown;
  treatment: string;
  treatment_owner: string;
  residual_score: unknown;
  review_due_at?: string;
  accepted_until?: string | null;
  accepter: string;
  instrument_reference: string;
  linked_control_ids: string[];
  created_at: string;
  updated_at: string;
};

export type RiskListResponse = { risks: DashboardRisk[]; count: number };

// ExpiringException mirrors `exceptionWire` in internal/api/exceptions/handlers.go.
export type ExpiringException = {
  id: string;
  control_id: string;
  justification: string;
  compensating_controls: string[];
  requested_by: string;
  requested_at: string;
  approved_by?: string;
  approved_at?: string;
  effective_from?: string;
  expires_at: string;
  expired_at?: string;
  status: string;
  created_at: string;
  updated_at: string;
};

export type ExpiringExceptionsResponse = {
  exceptions: ExpiringException[];
  count: number;
  within: string;
};

// FrameworkPostureRow mirrors `postureWire` in
// internal/api/dashboard/handler.go. One row per active framework version.
// `coverage_pct` and `freshness_composite` are 0-100; `trend_delta_90d`
// is signed (current coverage minus coverage 90 days ago, in points).
export type FrameworkPostureRow = {
  framework_id: string;
  framework_version: string;
  coverage_pct: number;
  freshness_composite: number;
  trend_delta_90d: number;
};

// FrameworkPostureReport mirrors the FrameworkPosture handler's envelope.
export type FrameworkPostureReport = {
  frameworks: FrameworkPostureRow[];
  count: number;
};

// UpcomingItem mirrors `upcomingWire` in
// internal/api/dashboard/handler.go. One row of the unified upcoming
// rollup — merges expiring exceptions, policy-ack expirations, vendor
// reviews, and audit-period milestones into one date-sorted feed.
// `category` is one of: exception / policy_ack / vendor_review /
// audit_period.
export type UpcomingItem = {
  due_date: string;
  category: string;
  title: string;
  resource_type: string;
  resource_id: string;
};

// UpcomingResponse mirrors the Upcoming handler's envelope.
// `next_cursor` is the opaque base64url keyset token for the next page,
// or "" when no next page exists.
export type UpcomingResponse = {
  upcoming: UpcomingItem[];
  count: number;
  next_cursor: string;
};

// ActivityEvent mirrors `activityWire` in internal/api/dashboard/handler.go.
// `summary` is forwarded as-is — the slice-062 admin_audit_log_v evidence
// branch packs an event-type-specific JSON blob whose shape is not
// uniformly renderable in the feed row; the panel cites the metadata
// triple (event_type / resource_type / resource_id) instead.
export type ActivityEvent = {
  ts: string;
  event_type: string;
  actor: string;
  resource_type: string;
  resource_id: string;
  summary: unknown;
};

// ActivityFeedResponse mirrors the Activity handler's envelope.
// `next_cursor` is the opaque base64url keyset token for the next page,
// or "" when no next page exists.
export type ActivityFeedResponse = {
  activity: ActivityEvent[];
  count: number;
  next_cursor: string;
};

// ----- server-side fns (called by the BFF route handlers) -----

export async function getControlDrift(
  bearer: string,
  since = "7d",
): Promise<DriftReport> {
  const res = await apiFetch(
    `/v1/controls/drift?since=${encodeURIComponent(since)}`,
    bearer,
  );
  return (await res.json()) as DriftReport;
}

export async function getEvidenceFreshness(
  bearer: string,
): Promise<FreshnessReport> {
  const res = await apiFetch(`/v1/evidence/freshness`, bearer);
  return (await res.json()) as FreshnessReport;
}

// getMitigateRisks fetches the dashboard top-risks panel feed —
// risks with treatment=mitigate, ranked by residual-score magnitude
// descending then risk age ascending. The `sort=residual,age` ordering
// is the slice-066 (AC-3) server-side capability that slice 157
// re-points the dashboard onto; before slice 157 the dashboard rendered
// the unsorted list with a labelled "ranking pending" footer.
export async function getMitigateRisks(
  bearer: string,
): Promise<DashboardRisk[]> {
  const res = await apiFetch(
    `/v1/risks?treatment=mitigate&sort=residual,age`,
    bearer,
  );
  const body = (await res.json()) as RiskListResponse;
  return body.risks;
}

export async function getExpiringExceptions(
  bearer: string,
  within = "30d",
): Promise<ExpiringExceptionsResponse> {
  const res = await apiFetch(
    `/v1/exceptions/expiring?within=${encodeURIComponent(within)}`,
    bearer,
  );
  return (await res.json()) as ExpiringExceptionsResponse;
}

export async function getFrameworkPosture(
  bearer: string,
): Promise<FrameworkPostureReport> {
  const res = await apiFetch(`/v1/frameworks/posture`, bearer);
  return (await res.json()) as FrameworkPostureReport;
}

export async function getActivity(
  bearer: string,
): Promise<ActivityFeedResponse> {
  const res = await apiFetch(`/v1/activity`, bearer);
  return (await res.json()) as ActivityFeedResponse;
}

// getUpcoming fetches the dashboard's unified upcoming-rollup feed —
// expiring exceptions, policy-ack expirations, vendor reviews, and
// audit-period milestones merged into one date-sorted (ascending)
// paginated feed. The slice-066 (AC-4) endpoint that slice 157
// re-points the dashboard onto; before slice 157 the dashboard rendered
// only the expiring-exceptions subset with a labelled "rollup pending"
// footer.
export async function getUpcoming(bearer: string): Promise<UpcomingResponse> {
  const res = await apiFetch(`/v1/upcoming`, bearer);
  return (await res.json()) as UpcomingResponse;
}

// ----- browser-side fns (hit the BFF under /api/dashboard/**) -----

export function fetchDashboardDrift(): Promise<DriftReport> {
  return bffControlFetch<DriftReport>(`/api/dashboard/drift`);
}

export function fetchDashboardFreshness(): Promise<FreshnessReport> {
  return bffControlFetch<FreshnessReport>(`/api/dashboard/freshness`);
}

export function fetchDashboardRisks(): Promise<DashboardRisk[]> {
  return bffControlFetch<RiskListResponse>(`/api/dashboard/risks`).then(
    (b) => b.risks,
  );
}

export function fetchDashboardUpcoming(): Promise<UpcomingResponse> {
  return bffControlFetch<UpcomingResponse>(`/api/dashboard/upcoming`);
}

export function fetchDashboardFrameworkPosture(): Promise<FrameworkPostureReport> {
  return bffControlFetch<FrameworkPostureReport>(
    `/api/dashboard/framework-posture`,
  );
}

export function fetchDashboardActivity(): Promise<ActivityFeedResponse> {
  return bffControlFetch<ActivityFeedResponse>(`/api/dashboard/activity`);
}
