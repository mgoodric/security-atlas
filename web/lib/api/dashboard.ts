// Slice 370 — program dashboard view (slice 040 + slice 147 re-point),
// extracted from the former `web/lib/api.ts` god-file.

import { apiFetch, bffControlFetch } from "./_shared";
import {
  type CalendarResponse,
  fetchCalendarEvents,
  getCalendarEvents,
} from "./calendar";

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
//   * Coming up          GET /v1/calendar, today + 14 days     (#2130) — bound
//       The compliance calendar is the one list of what is due; it
//       replaced the slice-066 upcoming rollup.
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

// GainRow mirrors `gainRowWire`: a control that started passing (#2063).
export type GainRow = {
  control_id: string;
};

// DriftReport mirrors the Drift handler's JSON envelope.
export type DriftReport = {
  since: string;
  through: string;
  delta: number;
  flipped_out_count: number;
  flipped_out: DriftRow[];
  started_passing_count: number;
  started_passing: GainRow[];
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

// COMING_UP_DAYS is the dashboard's "Coming up" window (#2130): the
// calendar from today (UTC) through the next 14 days. The Go dashboard
// export reads the same window (dashboardexport.UpcomingWindow).
export const COMING_UP_DAYS = 14;

// comingUpWindow returns the calendar query for the "Coming up" window
// at `now`: `from` is today's UTC date and `to` (inclusive on the
// calendar API) is 13 days later, so the window is [today, today + 14d).
export function comingUpWindow(now: Date = new Date()): {
  from: string;
  to: string;
} {
  const today = new Date(
    Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()),
  );
  const last = new Date(today.getTime() + (COMING_UP_DAYS - 1) * 86_400_000);
  return {
    from: today.toISOString().slice(0, 10),
    to: last.toISOString().slice(0, 10),
  };
}

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

// getComingUp fetches the dashboard's "Coming up" panel feed: the
// compliance calendar for the next 14 days (#2130).
export async function getComingUp(bearer: string): Promise<CalendarResponse> {
  return getCalendarEvents(bearer, comingUpWindow());
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

export function fetchDashboardComingUp(): Promise<CalendarResponse> {
  return fetchCalendarEvents(comingUpWindow());
}

export function fetchDashboardFrameworkPosture(): Promise<FrameworkPostureReport> {
  return bffControlFetch<FrameworkPostureReport>(
    `/api/dashboard/framework-posture`,
  );
}

export function fetchDashboardActivity(): Promise<ActivityFeedResponse> {
  return bffControlFetch<ActivityFeedResponse>(`/api/dashboard/activity`);
}
