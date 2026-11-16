// Slice 370 — /evidence list view (slice 099 + slice 106), extracted
// from the former `web/lib/api.ts` god-file.

import { APIError } from "./base";

// ----- Slice 099 + 106: /evidence list view (browser-side BFF call) -----
//
// Row source: `evidenceWire` in `internal/api/controldetail/handler.go`
// (the row shape `GET /v1/evidence` returns — both per-control and
// tenant-wide paths use the same wire shape). The page at
// `web/app/(authed)/evidence/page.tsx` calls `fetchEvidenceList` from
// the browser; the BFF at `web/app/api/evidence/route.ts` is the
// server-side counterpart that injects the bearer cookie (slice 094
// pattern) and forwards the whitelisted query params.
//
// Slice 106 changes:
//   * `result` is now on the GET wire shape (the column has always
//     existed on `evidence_records.result`; slice 064 omitted it).
//     The page renders the real result cell — no more em-dash
//     placeholder.
//   * `fetchEvidenceList` accepts an optional filter object. When
//     `controlID` is omitted the BFF + upstream serve the tenant-wide
//     ledger window (RLS continues to scope tenancy).

export type EvidenceResultEnum = "pass" | "fail" | "na" | "inconclusive";

export type EvidenceRecord = {
  evidence_id: string;
  evidence_kind: string | null;
  observed_at: string;
  // `source` is the slice-013 provenance JSONB verbatim. Shape varies
  // by connector — we render a summary client-side. `null` when the row
  // has no provenance metadata recorded.
  source: Record<string, unknown> | null;
  content_hash: string;
  scope_cell: string | null;
  /**
   * Slice 106: the `evidence_records.result` enum, surfaced on the GET
   * wire shape. One of pass | fail | na | inconclusive. Always set —
   * the column is NOT NULL on the table.
   */
  result: EvidenceResultEnum;
  /**
   * #2043 — true when the in-place demo seed added this record to the
   * organisation. Omitted for real evidence.
   */
  demo?: boolean;
};

export type EvidenceListResponse = {
  // Empty string when the request was tenant-wide (no control_id).
  control_id: string;
  evidence: EvidenceRecord[];
  count: number;
  /**
   * Slice 236: tenant-wide ledger row count (ignores filter predicates).
   * Surfaced so the `/evidence` page can render
   * `Showing N of M records` and disambiguate "ledger is empty" from
   * "my filters narrowed to zero". The query runs through the same
   * RLS-bound pool — tenant isolation is preserved.
   */
  total: number;
  next_cursor: string;
};

/**
 * Filter options for `fetchEvidenceList`. All fields are optional — an
 * empty object yields the tenant-wide ledger window.
 *
 * Slice 234 — `scopeCellID` and `since` join the existing set: the
 * `/evidence` filter row now ships six pills (Control, Kind, Result,
 * Source, Scope, Since).
 */
export type EvidenceListFilters = {
  controlID?: string;
  kind?: string;
  result?: EvidenceResultEnum;
  sourceActorType?: string;
  sourceActorID?: string;
  /**
   * Slice 234 — narrow the ledger to one scope cell. Server-side
   * intersection (the cell UUID's evidence rows). Out-of-tenant cells
   * return zero rows naturally via RLS.
   */
  scopeCellID?: string;
  /**
   * Slice 234 — RFC3339 timestamp; the upstream applies
   * `observed_at >= since`. The Since filter pill maps preset windows
   * ("Last 24 hours", "Last 7 days", "Last 30 days", "Audit period")
   * to a concrete RFC3339 cutoff client-side and passes it here.
   */
  since?: string;
  cursor?: string;
  limit?: number;
};

// Browser-side fetcher used by the slice-099 page. Hits the BFF at
// `/api/evidence[?...]` which forwards the bearer cookie to upstream
// `/v1/evidence[?...]`. Slice 106: signature changed from
// `fetchEvidenceList(controlID: string)` to
// `fetchEvidenceList(filters: EvidenceListFilters)` — when `controlID`
// is omitted the request returns the tenant-wide window.
export async function fetchEvidenceList(
  filters: EvidenceListFilters = {},
): Promise<EvidenceListResponse> {
  const qs = new URLSearchParams();
  if (filters.controlID) qs.set("control_id", filters.controlID);
  if (filters.kind) qs.set("kind", filters.kind);
  if (filters.result) qs.set("result", filters.result);
  if (filters.sourceActorType)
    qs.set("source_actor_type", filters.sourceActorType);
  if (filters.sourceActorID) qs.set("source_actor_id", filters.sourceActorID);
  // Slice 234 — new pills.
  if (filters.scopeCellID) qs.set("scope_cell_id", filters.scopeCellID);
  if (filters.since) qs.set("since", filters.since);
  if (filters.cursor) qs.set("cursor", filters.cursor);
  if (filters.limit) qs.set("limit", String(filters.limit));

  const url = qs.toString()
    ? `/api/evidence?${qs.toString()}`
    : `/api/evidence`;
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
  return (await res.json()) as EvidenceListResponse;
}

// ----- #2137: /evidence/[id] detail view -----
//
// Row source: `evidenceDetailWire` in
// `internal/api/controldetail/evidence_detail.go` (`GET /v1/evidence/{id}`).
// The payload is already redacted server-side; the browser renders it as
// served. A caller without access gets a 404, the same as an unknown id.

export type EvidenceDetail = {
  evidence_id: string;
  evidence_kind: string | null;
  schema_version: string | null;
  observed_at: string;
  ingested_at: string;
  result: EvidenceResultEnum;
  payload: unknown;
  payload_uri: string | null;
  source: Record<string, unknown> | null;
  source_attribution: Record<string, unknown> | null;
  credential_id: string | null;
  ingestion_path: string;
  control_id: string | null;
  control_ref: string;
  scope_cell: string | null;
  content_hash: string;
  demo?: boolean;
};

export type EvidenceDetailResponse = { evidence: EvidenceDetail };

// Browser-side fetcher for the detail page. Hits the BFF at
// `/api/evidence/{id}`, which forwards the bearer cookie upstream.
export async function fetchEvidenceDetail(
  id: string,
): Promise<EvidenceDetailResponse> {
  const res = await fetch(`/api/evidence/${encodeURIComponent(id)}`);
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
  return (await res.json()) as EvidenceDetailResponse;
}
