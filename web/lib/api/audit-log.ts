// Slice 125 — typed client for the unified audit-log endpoint.
//
// The shape mirrors `internal/api/adminauditlog/unified.go`:
//   * `UnifiedEntry`        — one row of the audit-log union
//   * `UnifiedListResponse` — wrapper with `entries` + opaque `next_cursor`
//
// `actor_id` is included verbatim.
//
// Slice 129: `actor_name` is the human-readable display name resolved via
// LEFT JOIN against `users.display_name` on the backend. It is `null` when
// no users row matches the actor_id (bootstrap-key callers, credential-only
// callers, system actors like 'seeder'). The page renders `actor_name` when
// present and falls back to the truncated `actor_id` otherwise. The field is
// also `undefined` for older deployments that predate slice 129 — the page
// MUST gracefully degrade to the actor_id truncation in that case (P0-A6).
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
  // Slice 129: human-readable display name resolved via LEFT JOIN onto
  // `users.display_name` under the caller's tenant context (RLS enforced).
  // - `string` — a users row matched the actor_id (the actor is a real user).
  // - `null`   — backend served the field but no users row matched (the
  //              normal case for credential-only / bootstrap-key / system
  //              actors whose actor_id is not a UUID or does not resolve).
  // - `undefined` — backend predates slice 129 (older deployment); the page
  //                 falls back to the truncated actor_id (P0-A6).
  actor_name?: string | null;
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

/**
 * truncateActorId returns the first 8 characters of an actor identifier
 * followed by an ellipsis. The page uses this as the cell-text fallback
 * when no `actor_name` is available. Exported so the vitest suite can
 * lock the truncation contract.
 */
export function truncateActorId(id: string): string {
  if (!id) return "(none)";
  if (id.length <= 8) return id;
  return `${id.slice(0, 8)}…`;
}

/**
 * renderActorLabel chooses what to render in the actor column for one
 * audit-log row. Slice 129 contract:
 *
 *   - When the backend resolved a `users` row, return the display name
 *     (`actor_name`).
 *   - Otherwise (no users row, or older deployment whose backend predates
 *     slice 129 and never serves the field — P0-A6 graceful-degrade)
 *     fall back to `truncateActorId(actor_id)`.
 *
 * `null` and `undefined` are treated identically — both mean "no resolved
 * name available; render the actor_id fallback".
 */
export function renderActorLabel(row: UnifiedEntry): string {
  const name = row.actor_name;
  if (name && name.length > 0) return name;
  return truncateActorId(row.actor_id);
}
