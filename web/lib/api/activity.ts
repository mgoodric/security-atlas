// Slice 270 — typed client for the non-admin activity-ledger endpoint.
//
// The wire shape mirrors `internal/api/adminauditlog/activity.go`, which
// reuses the slice-124 UnifiedListResponse + UnifiedEntry shapes. We
// re-export those types from `lib/api/audit-log.ts` so the activity
// page consumes the same types as the audit-log page (slice 270 D3 —
// future refactor extracts a shared component using these types).
//
// The differences between the two endpoints are:
//
//   - BFF path: `/api/activity` vs `/api/audit-log/unified`.
//   - Default `actor` filter: the activity page understands a `me`
//     sentinel that the BFF resolves to the caller's user_id (slice
//     270 D5). The audit-log page does not.
//
// Both endpoints accept the same kind, from/to, actor, and cursor
// query parameters and return the same UnifiedListResponse shape.

import {
  AUDIT_LOG_KINDS,
  AuditLogFetchError,
  AuditLogKind,
  MAX_WINDOW_DAYS,
  UnifiedEntry,
  UnifiedListParams,
  UnifiedListResponse,
  buildUnifiedQuery,
  renderActorLabel,
  truncateActorId,
} from "@/lib/api/audit-log";

export {
  AUDIT_LOG_KINDS as ACTIVITY_KINDS,
  AuditLogFetchError as ActivityFetchError,
  type AuditLogKind as ActivityKind,
  MAX_WINDOW_DAYS,
  type UnifiedEntry as ActivityEntry,
  type UnifiedListParams as ActivityListParams,
  type UnifiedListResponse as ActivityListResponse,
  buildUnifiedQuery as buildActivityQuery,
  renderActorLabel,
  truncateActorId,
};

// ACTOR_ME_SENTINEL — when the URL carries `actor=me`, the BFF resolves
// the literal to the caller's user_id before forwarding (slice 270 D5).
// Re-exported here so the page client can detect the sentinel and render
// the friendlier "(your activity)" label without needing the resolved
// UUID on the client.
export const ACTOR_ME_SENTINEL = "me";

/**
 * Browser-side fetch helper for the slice 270 BFF. Returns the parsed
 * JSON envelope or throws on non-2xx with the upstream error message
 * attached.
 */
export async function fetchActivity(
  params: UnifiedListParams,
  signal?: AbortSignal,
): Promise<UnifiedListResponse> {
  const res = await fetch(`/api/activity${buildUnifiedQuery(params)}`, {
    cache: "no-store",
    signal,
  });
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
