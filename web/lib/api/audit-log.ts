// Slice 125 — typed client for the unified audit-log endpoint.
//
// The shape mirrors `internal/api/adminauditlog/unified.go`:
//   * `UnifiedEntry`        — one row of the audit-log union
//   * `UnifiedListResponse` — wrapper with `entries` + opaque `next_cursor`
//
// `actor_id` is included verbatim. Per the slice-125 decisions log (D1), the
// frontend renders a truncated 8-char prefix because the slice-124 endpoint
// does NOT join the actor row's display name. A spillover slice (filed
// alongside this slice) extends slice 124 with `actor_name` so the page can
// upgrade to the human-readable label without a per-row N+1 client lookup.
//
// `kind` is the canonical 9-value enum from the platform side
// (see `internal/audit/unifiedlog/Kind`); the union is kept loose (`string`)
// here because adding a 10th kind backend-side must NOT break the typed
// client at compile time.

export const AUDIT_LOG_KINDS = [
  "decision",
  "evidence",
  "exception",
  "sample",
  "audit_period",
  "aggregation_rule",
  "feature_flag",
  "me",
  "walkthrough",
] as const;

export type AuditLogKind = (typeof AUDIT_LOG_KINDS)[number];

export type UnifiedEntry = {
  occurred_at: string; // RFC3339
  actor_id: string;
  tenant_id: string;
  kind: string; // typically AuditLogKind, but kept loose for forward-compat
  target_type: string;
  target_id: string;
  action: string;
  row_id: string;
  payload_json: unknown;
};

export type UnifiedListResponse = {
  entries: UnifiedEntry[];
  next_cursor?: string;
};

export type UnifiedListParams = {
  from: string; // RFC3339
  to: string; // RFC3339
  actor?: string;
  kinds?: AuditLogKind[];
  cursor?: string;
};

/**
 * Build the canonical `?from=...&to=...&actor=...&kind=a,b&cursor=...` query
 * string the BFF (and downstream platform handler) expect. Empty / undefined
 * optional fields are dropped so the URL stays human-readable.
 */
export function buildUnifiedQuery(params: UnifiedListParams): string {
  const u = new URLSearchParams();
  u.set("from", params.from);
  u.set("to", params.to);
  if (params.actor && params.actor.trim()) {
    u.set("actor", params.actor.trim());
  }
  if (params.kinds && params.kinds.length > 0) {
    u.set("kind", params.kinds.join(","));
  }
  if (params.cursor && params.cursor.trim()) {
    u.set("cursor", params.cursor.trim());
  }
  return `?${u.toString()}`;
}

/**
 * Browser-side fetch helper for the BFF. Returns the parsed JSON envelope
 * (or throws on non-2xx with the upstream error message attached).
 */
export async function fetchUnifiedAuditLog(
  params: UnifiedListParams,
  signal?: AbortSignal,
): Promise<UnifiedListResponse> {
  const res = await fetch(
    `/api/audit-log/unified${buildUnifiedQuery(params)}`,
    {
      cache: "no-store",
      signal,
    },
  );
  if (!res.ok) {
    let detail = "";
    try {
      const body = (await res.json()) as { error?: string };
      detail = body.error ?? "";
    } catch {
      // body is empty / non-JSON — fall through with status-only detail
    }
    throw new AuditLogFetchError(res.status, detail);
  }
  return (await res.json()) as UnifiedListResponse;
}

export class AuditLogFetchError extends Error {
  status: number;
  constructor(status: number, detail: string) {
    super(detail ? `${status} ${detail}` : `${status}`);
    this.name = "AuditLogFetchError";
    this.status = status;
  }
}

/**
 * The slice-124 backend caps the window at 90 days. The page enforces the
 * same cap in the date-picker; this constant is the single source of truth.
 */
export const MAX_WINDOW_DAYS = 90;
