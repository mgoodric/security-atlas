// Slice 370 — /exceptions list view (slice 177), extracted from the
// former `web/lib/api.ts` god-file. Named `exceptions.ts`; distinct from
// the pre-existing `lib/api/exceptions-export.ts` (data-export client).

import { APIError } from "./base";

// ----- Slice 177: /exceptions list view -----
//
// Row source: `exceptionWire` in `internal/api/exceptions/handlers.go`
// (slice 021 / slice 138 wire shape — same row that the slice 138
// exceptions data-export materialises). The page at
// `web/app/(authed)/exceptions/page.tsx` calls `fetchExceptionsList`
// from the browser; the BFF at `web/app/api/exceptions/route.ts` is the
// server-side counterpart that injects the bearer cookie (slice 094
// pattern). Mirrors the slice 098 controls / slice 099 evidence shape.
//
// Filter parameters whitelisted on the BFF + this fetcher:
//   - status (requested / approved / denied / active / expired)
//   - control_id (UUID — narrows to one anchor's exceptions)
//
// All other params (`tenant_id`, debug flags, etc.) are dropped at the
// BFF. RLS enforces tenant isolation at the DB layer per invariant 6.

/**
 * Status enum for exception rows. Mirrors the constants in
 * `internal/exception/store.go` (StateRequested / StateApproved /
 * StateDenied / StateActive / StateExpired). Lifecycle:
 *   requested → approved → active → expired
 *                       ↘ denied
 */
export type ExceptionStatus =
  | "requested"
  | "approved"
  | "denied"
  | "active"
  | "expired";

/**
 * Exception is the canonical wire shape for an exception/waiver row.
 * Mirrors `exceptionWire` in `internal/api/exceptions/handlers.go`. The
 * optional timestamps reflect the lifecycle: `approved_at` is set after
 * approve, `effective_from` after activate, `expired_at` after the
 * row's expiry sweep ran. `justification` is sensitive (slice 138
 * P0-A-Ledger-3) — surfaced in the table truncated, full text only in
 * row drawer.
 */
export type Exception = {
  id: string;
  control_id: string;
  scope_cell_predicate: unknown;
  justification: string;
  compensating_controls: string[];
  requested_by: string;
  requested_at: string;
  approved_by?: string;
  approved_at?: string;
  denied_by?: string;
  denied_at?: string;
  activated_by?: string;
  activated_at?: string;
  effective_from?: string;
  expires_at: string;
  expired_at?: string;
  status: ExceptionStatus;
  created_at: string;
  updated_at: string;
};

export type ExceptionsListResponse = {
  exceptions: Exception[];
  count: number;
};

/**
 * Filter options for `fetchExceptionsList`. All fields are optional —
 * an empty object yields the tenant-wide list (RLS-scoped).
 */
export type ExceptionsListFilters = {
  status?: ExceptionStatus | "";
  controlId?: string;
};

/**
 * Browser-side fetcher used by the slice 177 page. Hits the BFF at
 * `/api/exceptions[?...]` which forwards the bearer cookie to upstream
 * `/v1/exceptions[?...]`. The bearer never reaches the browser.
 */
export async function fetchExceptionsList(
  filters: ExceptionsListFilters = {},
): Promise<ExceptionsListResponse> {
  const qs = new URLSearchParams();
  if (filters.status) qs.set("status", filters.status);
  if (filters.controlId) qs.set("control_id", filters.controlId);

  const url = qs.toString()
    ? `/api/exceptions?${qs.toString()}`
    : `/api/exceptions`;
  const res = await fetch(url);
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      /* body not JSON — keep the status line */
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as ExceptionsListResponse;
}
