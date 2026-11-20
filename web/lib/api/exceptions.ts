// Slice 370 — /exceptions list view (slice 177), extracted from the
// former `web/lib/api.ts` god-file. Named `exceptions.ts`; distinct from
// the pre-existing `lib/api/exceptions-export.ts` (data-export client).

import { APIError } from "./base";
import { upstreamErrorMessage } from "./_shared";

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
//   - status (requested / approved / denied / active / expired / revoked)
//   - control_id (UUID — narrows to one anchor's exceptions)
//
// All other params (`tenant_id`, debug flags, etc.) are dropped at the
// BFF. RLS enforces tenant isolation at the DB layer per invariant 6.

/**
 * Status enum for exception rows. Mirrors the constants in
 * `internal/exception/store.go` (StateRequested / StateApproved /
 * StateDenied / StateActive / StateExpired / StateRevoked). Lifecycle:
 *   requested → approved → active → expired
 *           ↘ denied               ↘ revoked (#2113)
 */
export type ExceptionStatus =
  | "requested"
  | "approved"
  | "denied"
  | "active"
  | "expired"
  | "revoked";

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
  /**
   * #2101 — true when the requester approved the exception under the
   * organisation's Allow self-approval setting.
   */
  self_approved?: boolean;
  denied_by?: string;
  denied_at?: string;
  activated_by?: string;
  activated_at?: string;
  effective_from?: string;
  expires_at: string;
  expired_at?: string;
  /** #2113 — set when an approver revoked the active exception. */
  revoked_by?: string;
  revoked_at?: string;
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
    const msg = await upstreamErrorMessage(res);
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as ExceptionsListResponse;
}

// ----- #2113: one exception, its audit log, and its lifecycle actions -----
//
// Each call goes through a BFF under `/api/exceptions/...`, which forwards
// the bearer cookie. The platform owns every refusal (403 for a role or
// the approval rule, 409 for a wrong state); these fetchers surface the
// platform's message as an APIError.

export type ExceptionResponse = { exception: Exception };

export type ExceptionAuditEntry = {
  id: string;
  action: string;
  actor: string;
  from_state?: string;
  to_state: string;
  reason: string;
  occurred_at: string;
  exception_id: string;
};

export type ExceptionAuditLogResponse = {
  entries: ExceptionAuditEntry[];
  count: number;
};

/** The request body for POST /v1/exceptions. */
export type CreateExceptionInput = {
  control_id: string;
  /**
   * A scope predicate object. Never null: the scope picker emits null for
   * an incomplete choice, and the platform reads a stored null as every
   * cell. The whole control is `{"op":"true"}`.
   */
  scope_cell_predicate: object;
  justification: string;
  compensating_controls: string[];
  /** RFC 3339. At most 365 days after the request. */
  expires_at: string;
};

/** The lifecycle actions a person takes on an exception. */
export type ExceptionTransition = "approve" | "deny" | "activate" | "revoke";

async function throwAPIError(res: Response): Promise<never> {
  const msg = await upstreamErrorMessage(res);
  throw new APIError(res.status, msg);
}

function exceptionPath(id: string): string {
  return `/api/exceptions/${encodeURIComponent(id)}`;
}

export async function fetchException(id: string): Promise<ExceptionResponse> {
  const res = await fetch(exceptionPath(id));
  if (!res.ok) return throwAPIError(res);
  return (await res.json()) as ExceptionResponse;
}

export async function fetchExceptionAuditLog(
  id: string,
): Promise<ExceptionAuditLogResponse> {
  const res = await fetch(`${exceptionPath(id)}/audit-log`);
  if (!res.ok) return throwAPIError(res);
  return (await res.json()) as ExceptionAuditLogResponse;
}

/**
 * Files an exception. Refuses, before any request, a predicate that is
 * null or not an object, so an incomplete scope choice is never sent.
 *
 * #2618: `idempotencyKey` goes out as the Idempotency-Key header. Send the
 * same key when retrying the same request, and the platform returns the
 * exception the first send filed instead of filing a duplicate.
 */
export async function createException(
  input: CreateExceptionInput,
  idempotencyKey?: string,
): Promise<ExceptionResponse> {
  const p: unknown = input.scope_cell_predicate;
  if (p === null || typeof p !== "object" || Array.isArray(p)) {
    throw new Error(
      "Choose the scope this exception covers before requesting it.",
    );
  }
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };
  if (idempotencyKey) headers["Idempotency-Key"] = idempotencyKey;
  const res = await fetch("/api/exceptions", {
    method: "POST",
    headers,
    body: JSON.stringify(input),
  });
  if (!res.ok) return throwAPIError(res);
  return (await res.json()) as ExceptionResponse;
}

/**
 * Runs one lifecycle action. `body` carries the deny or revoke `reason`,
 * or the activate `effective_from`; approve takes none.
 */
export async function transitionException(
  id: string,
  transition: ExceptionTransition,
  body: { reason?: string; effective_from?: string } = {},
): Promise<ExceptionResponse> {
  const res = await fetch(`${exceptionPath(id)}/${transition}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) return throwAPIError(res);
  return (await res.json()) as ExceptionResponse;
}
