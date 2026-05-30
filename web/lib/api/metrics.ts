// Slice 097 — metrics dashboard + cascade-tree view.
//
// Binds the seven slice-076 endpoints into the web frontend. The wire
// shapes mirror `internal/api/metrics/handlers.go` exactly — every field
// is typed straight from the Go `metricWire` / `cascadeNodeWire` /
// `observationWire` / `inputWire` / `targetWire` structs so this file
// stays the single source of truth for the browser side.
//
// Endpoint inventory (verified 2026-05-16 against
// `internal/api/httpserver.go`):
//
//   * GET  /v1/metrics                     ListCatalog  (level/category filter)
//   * GET  /v1/metrics/cascade             GetCascade   (level + depth)
//   * GET  /v1/metrics/{id}                GetCatalog   (metric + parents + children)
//   * GET  /v1/metrics/{id}/observations   ListObservations (since/until/limit)
//   * POST /v1/metrics/{id}/inputs         CreateInput  (admin-only)
//   * GET  /v1/metrics/{id}/target         GetTarget
//   * PUT  /v1/metrics/{id}/target         UpsertTarget (admin-only)
//
// Browser-side calls go through the BFF under `/api/metrics/**` (slice
// 040 pattern) so the bearer cookie never leaves the server.

import { apiBaseURL, APIError } from "@/lib/api/base";

// ===== wire shapes =====

export type MetricLevel = "board" | "program" | "team";

export type MetricComputeStrategy =
  | "manual_input"
  | "evaluator"
  | "external_integration"
  | "rollup";

export type Metric = {
  id: string;
  level: string;
  category: string;
  name: string;
  description: string;
  unit: string;
  cadence: string;
  compute_strategy: string;
  compute_evaluator?: string;
  source_slices: string[];
  notes?: string;
};

export type MetricDetail = {
  metric: Metric;
  parents: Metric[];
  children: Metric[];
};

export type CascadeNode = {
  metric_id: string;
  parent_id?: string;
  depth: number;
};

export type CascadeResponse = {
  nodes: CascadeNode[];
  count: number;
  depth: number;
  truncated: boolean;
  root_level: string;
};

export type Observation = {
  id: string;
  metric_id: string;
  observed_at: string;
  numeric_value: string;
  dimensions: Record<string, unknown>;
  source: string;
  created_at: string;
};

export type ObservationsPage = {
  observations: Observation[];
  count: number;
};

export type MetricInput = {
  id: string;
  metric_id: string;
  input_at: string;
  numeric_value: string;
  dimensions: Record<string, unknown>;
  entered_by_user_id: string;
  notes?: string;
};

export type MetricInputCreate = {
  numeric_value: number;
  observed_at?: string;
  dimensions?: Record<string, unknown>;
  notes?: string;
};

export type MetricTarget = {
  metric_id: string;
  target_value?: string;
  warning_threshold?: string;
  critical_threshold?: string;
  // higher_is_better | lower_is_better | target_is_better
  direction: string;
  owner_user_id?: string;
  notes?: string;
};

export type MetricTargetUpsert = {
  target_value?: number;
  warning_threshold?: number;
  critical_threshold?: number;
  direction: string;
  owner_user_id?: string;
  notes?: string;
};

// ===== shared fetch helper (server-side, bearer-injecting) =====

async function metricsServerFetch(
  path: string,
  bearer: string,
  init?: RequestInit,
): Promise<Response> {
  const res = await fetch(`${apiBaseURL()}${path}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${bearer}`,
      ...(init?.headers ?? {}),
    },
    cache: "no-store",
  });
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  return res;
}

// ===== server-side fns (called by BFF route handlers / RSC) =====

export async function listMetrics(
  bearer: string,
  filter?: { level?: string; category?: string },
): Promise<Metric[]> {
  const qs = new URLSearchParams();
  if (filter?.level) qs.set("level", filter.level);
  if (filter?.category) qs.set("category", filter.category);
  const suffix = qs.toString() ? `?${qs.toString()}` : "";
  const res = await metricsServerFetch(`/v1/metrics${suffix}`, bearer);
  const body = (await res.json()) as { metrics: Metric[]; count: number };
  return body.metrics ?? [];
}

export async function getMetric(
  bearer: string,
  id: string,
): Promise<MetricDetail> {
  const res = await metricsServerFetch(
    `/v1/metrics/${encodeURIComponent(id)}`,
    bearer,
  );
  return (await res.json()) as MetricDetail;
}

export async function getCascade(
  bearer: string,
  level: string = "board",
  depth?: number,
): Promise<CascadeResponse> {
  const qs = new URLSearchParams({ level });
  if (depth !== undefined) qs.set("depth", String(depth));
  const res = await metricsServerFetch(
    `/v1/metrics/cascade?${qs.toString()}`,
    bearer,
  );
  return (await res.json()) as CascadeResponse;
}

export async function listObservations(
  bearer: string,
  id: string,
  opts?: { since?: string; until?: string; limit?: number },
): Promise<ObservationsPage> {
  const qs = new URLSearchParams();
  if (opts?.since) qs.set("since", opts.since);
  if (opts?.until) qs.set("until", opts.until);
  if (opts?.limit) qs.set("limit", String(opts.limit));
  const suffix = qs.toString() ? `?${qs.toString()}` : "";
  const res = await metricsServerFetch(
    `/v1/metrics/${encodeURIComponent(id)}/observations${suffix}`,
    bearer,
  );
  return (await res.json()) as ObservationsPage;
}

export async function createInput(
  bearer: string,
  id: string,
  body: MetricInputCreate,
): Promise<MetricInput> {
  const res = await metricsServerFetch(
    `/v1/metrics/${encodeURIComponent(id)}/inputs`,
    bearer,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    },
  );
  return (await res.json()) as MetricInput;
}

export async function getTarget(
  bearer: string,
  id: string,
): Promise<MetricTarget | null> {
  // 404 = "no target set" — the catalog row exists but no tenant-side
  // target has been configured. Surface that as `null` instead of an
  // error so the dashboard can render a "No target set" affordance.
  const res = await fetch(
    `${apiBaseURL()}/v1/metrics/${encodeURIComponent(id)}/target`,
    {
      headers: { Authorization: `Bearer ${bearer}` },
      cache: "no-store",
    },
  );
  if (res.status === 404) return null;
  if (!res.ok) {
    throw new APIError(res.status, `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as MetricTarget;
}

export async function upsertTarget(
  bearer: string,
  id: string,
  body: MetricTargetUpsert,
): Promise<MetricTarget> {
  const res = await metricsServerFetch(
    `/v1/metrics/${encodeURIComponent(id)}/target`,
    bearer,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    },
  );
  return (await res.json()) as MetricTarget;
}

// ===== browser-side fns (hit the BFF under /api/metrics/**) =====
//
// `bffFetch` is a narrow helper that mirrors `bffControlFetch` in
// lib/api.ts: passes the BFF status + JSON body verbatim, raises
// `APIError` on non-2xx.

async function bffFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, init);
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

export function fetchMetricsCatalog(filter?: {
  level?: string;
  category?: string;
}): Promise<Metric[]> {
  const qs = new URLSearchParams();
  if (filter?.level) qs.set("level", filter.level);
  if (filter?.category) qs.set("category", filter.category);
  const suffix = qs.toString() ? `?${qs.toString()}` : "";
  return bffFetch<{ metrics: Metric[]; count: number }>(
    `/api/metrics${suffix}`,
  ).then((b) => b.metrics ?? []);
}

export function fetchMetric(id: string): Promise<MetricDetail> {
  return bffFetch<MetricDetail>(`/api/metrics/${encodeURIComponent(id)}`);
}

export function fetchCascade(
  level: string = "board",
  depth?: number,
): Promise<CascadeResponse> {
  const qs = new URLSearchParams({ level });
  if (depth !== undefined) qs.set("depth", String(depth));
  return bffFetch<CascadeResponse>(`/api/metrics/cascade?${qs.toString()}`);
}

export function fetchObservations(
  id: string,
  opts?: { since?: string; until?: string; limit?: number },
): Promise<ObservationsPage> {
  const qs = new URLSearchParams();
  if (opts?.since) qs.set("since", opts.since);
  if (opts?.until) qs.set("until", opts.until);
  if (opts?.limit) qs.set("limit", String(opts.limit));
  const suffix = qs.toString() ? `?${qs.toString()}` : "";
  return bffFetch<ObservationsPage>(
    `/api/metrics/${encodeURIComponent(id)}/observations${suffix}`,
  );
}

export function fetchTarget(id: string): Promise<MetricTarget | null> {
  // The BFF route returns 200 with `null` body when upstream is 404, so
  // the browser side never has to special-case status codes here.
  return bffFetch<MetricTarget | null>(
    `/api/metrics/${encodeURIComponent(id)}/target`,
  );
}

export function submitInput(
  id: string,
  body: MetricInputCreate,
): Promise<MetricInput> {
  return bffFetch<MetricInput>(
    `/api/metrics/${encodeURIComponent(id)}/inputs`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    },
  );
}

export function upsertMetricTarget(
  id: string,
  body: MetricTargetUpsert,
): Promise<MetricTarget> {
  return bffFetch<MetricTarget>(
    `/api/metrics/${encodeURIComponent(id)}/target`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    },
  );
}

// ===== pure logic — exposed for vitest =====

// CascadeTreeNode is a parent-child tree built from the flat cascade
// node list returned by GET /v1/metrics/cascade. The root is a synthetic
// node whose `metric_id` is "" so callers can walk children from a
// single entry point. Roots in the upstream payload are real metrics
// with `parent_id` empty/undefined.
export type CascadeTreeNode = {
  metric_id: string;
  depth: number;
  children: CascadeTreeNode[];
};

// reassembleCascade walks a flat `nodes` list (sorted or not) and builds
// a tree of CascadeTreeNode. Cycle resistance: the recursive CTE on the
// platform side caps depth (MaxCascadeDepth=6) and never returns a
// node twice. This function is therefore safe to consume the upstream
// payload as-is. Two callers depend on the deterministic ordering of
// children — render in the order the upstream returned, which mirrors
// the DB query's ORDER BY (depth, metric_id).
export function reassembleCascade(nodes: CascadeNode[]): CascadeTreeNode[] {
  const map = new Map<string, CascadeTreeNode>();
  for (const n of nodes) {
    map.set(n.metric_id, {
      metric_id: n.metric_id,
      depth: n.depth,
      children: [],
    });
  }
  const roots: CascadeTreeNode[] = [];
  for (const n of nodes) {
    const tree = map.get(n.metric_id)!;
    if (n.parent_id && map.has(n.parent_id)) {
      map.get(n.parent_id)!.children.push(tree);
    } else {
      roots.push(tree);
    }
  }
  return roots;
}

// ThresholdBadgeColor is the green / yellow / red state for a single
// metric value against its target row. Pure function so vitest can
// exercise every branch.
//
// Rules (per AC-3):
//   * No target set                          -> "green"  (nothing to fail against)
//   * No observation yet                     -> "neutral"
//   * direction=higher_is_better:
//       value >= target           -> "green"
//       value < target  but value >= warning  -> "yellow"
//       value < critical                        -> "red"
//   * direction=lower_is_better (inverse)
//   * direction=target_is_better — within +/-10% band of target -> green,
//       between band and warning -> yellow, beyond critical -> red.
//
// The threshold values arriving from the wire are strings (numeric
// columns are pgtype.Numeric → JSON strings). Parsing is done here.
export type ThresholdColor = "green" | "yellow" | "red" | "neutral";

function parseNum(s?: string | null): number | undefined {
  if (s === undefined || s === null || s === "") return undefined;
  const n = Number(s);
  return Number.isFinite(n) ? n : undefined;
}

export function thresholdBadgeColor(
  value: number | undefined,
  target: Pick<
    MetricTarget,
    "target_value" | "warning_threshold" | "critical_threshold" | "direction"
  > | null,
): ThresholdColor {
  if (value === undefined) return "neutral";
  if (!target) return "green";
  const t = parseNum(target.target_value);
  const w = parseNum(target.warning_threshold);
  const c = parseNum(target.critical_threshold);
  if (t === undefined) return "green";
  if (target.direction === "higher_is_better") {
    if (c !== undefined && value <= c) return "red";
    if (value >= t) return "green";
    if (w !== undefined && value >= w) return "yellow";
    return "red";
  }
  if (target.direction === "lower_is_better") {
    if (c !== undefined && value >= c) return "red";
    if (value <= t) return "green";
    if (w !== undefined && value <= w) return "yellow";
    return "red";
  }
  // target_is_better: closeness to target. Use the warning_threshold as
  // the inner band's half-width; critical as the outer band.
  const innerBand = w !== undefined ? Math.abs(w - t) : Math.abs(t) * 0.05;
  const outerBand = c !== undefined ? Math.abs(c - t) : Math.abs(t) * 0.15;
  const distance = Math.abs(value - t);
  if (distance <= innerBand) return "green";
  if (distance <= outerBand) return "yellow";
  return "red";
}
