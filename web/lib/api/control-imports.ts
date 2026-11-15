// #1785 — wire types and pure view helpers for the control import review
// page. The types mirror internal/controlimport (Batch, BatchControl,
// Candidate, RowList, RowDecision, ApproveClearResult); the helpers are
// node-testable so the page stays a renderer.

import { APIError } from "./base";

export type Requirement = { code: string; requirement_id: string };

export type CandidateSource = "crosswalk" | "keyword" | "llm";

export type Candidate = {
  rank: number;
  scf_anchor_id: string;
  scf_id: string;
  title: string;
  source: CandidateSource | string;
  reaches: Requirement[] | null;
  tested_count: number;
  strength?: number;
  ai_assisted: boolean;
  rationale?: string;
};

export type RowStatus = "pending" | "approved" | "rejected" | "duplicate";

export const ROW_STATUSES: readonly RowStatus[] = [
  "pending",
  "approved",
  "rejected",
  "duplicate",
];

export const ROW_STATUS_LABELS: Record<RowStatus, string> = {
  pending: "Pending",
  approved: "Approved",
  rejected: "Rejected",
  duplicate: "Duplicate",
};

export type BatchRow = {
  id: string;
  row_number: number;
  external_id: string;
  title: string;
  description: string;
  tested_under: Requirement[] | null;
  implementation_type?: string;
  applicability?: string;
  status: RowStatus | string;
  candidates: Candidate[] | null;
  no_candidate_reaches: boolean;
  llm_rerank: string;
  llm_provider?: string;
  decided_by?: string;
  control_id?: string;
  approved_scf_id?: string;
  rejection_reason?: string;
};

export type RejectedRow = {
  row_number: number;
  external_id?: string;
  reason: string;
  detail: string;
};

export type ImportBatch = {
  id: string;
  framework: { slug: string; version_id: string; version: string };
  source_filename: string;
  source_format: string;
  uploaded_by: string;
  status: string;
  total_rows: number;
  staged_count: number;
  rejected_count: number;
  unmapped_columns: string[] | null;
  scf_release: { version_id: string; version: string } | null;
  suggestions_status: string;
  created_at: string;
  staged: BatchRow[] | null;
  rejected: RejectedRow[] | null;
};

export type RowList = {
  batch_id: string;
  scf_release: { version_id: string; version: string } | null;
  suggestions_status: string;
  rows: BatchRow[] | null;
};

export type ApproveClearResult = {
  batch_id: string;
  qualifying: number;
  approved_count: number;
  approved: unknown[] | null;
  duplicates: unknown[] | null;
  skipped: { row_number: number; external_id: string; reason: string }[] | null;
};

export const IMPLEMENTATION_TYPES = [
  "automated",
  "semi_automated",
  "manual_attested",
  "manual_periodic",
] as const;

export const IMPLEMENTATION_TYPE_LABELS: Record<string, string> = {
  automated: "Automated",
  semi_automated: "Semi-automated",
  manual_attested: "Manual (attested)",
  manual_periodic: "Manual (periodic)",
};

/** The implementation type an approval without an override gets:
 *  the sheet's, else the platform default (DefaultImplementationType). */
export function effectiveImplementationType(row: BatchRow): string {
  return row.implementation_type || "manual_attested";
}

export type BatchProgress = {
  staged: number;
  approved: number;
  rejected: number;
  duplicate: number;
  pending: number;
  /** Rows the upload itself refused (missing title, duplicate ID, ...). */
  notStaged: number;
};

/** batchProgress tallies a batch's staged rows by review status. */
export function batchProgress(batch: ImportBatch): BatchProgress {
  const p: BatchProgress = {
    staged: batch.staged_count,
    approved: 0,
    rejected: 0,
    duplicate: 0,
    pending: 0,
    notStaged: batch.rejected_count,
  };
  for (const row of batch.staged ?? []) {
    if (row.status === "approved") p.approved++;
    else if (row.status === "rejected") p.rejected++;
    else if (row.status === "duplicate") p.duplicate++;
    else p.pending++;
  }
  return p;
}

/** fitLabel renders "reaches 2 of 3: CC6.1, CC6.2", the server's
 *  Candidate.Fit. Empty for a row with no tested criteria. */
export function fitLabel(c: Candidate): string {
  if (c.tested_count <= 0) return "";
  const reaches = c.reaches ?? [];
  const head = `reaches ${reaches.length} of ${c.tested_count}`;
  if (reaches.length === 0) return head;
  return `${head}: ${reaches.map((r) => r.code).join(", ")}`;
}

/** sourceLabel names where a candidate came from. A candidate the LLM
 *  placed is labelled as reranked whatever its deterministic source. */
export function sourceLabel(c: Candidate): string {
  if (c.ai_assisted || c.source === "llm") return "LLM-reranked";
  if (c.source === "crosswalk") return "Crosswalk";
  if (c.source === "keyword") return "Keyword";
  return c.source;
}

/** topCandidates returns the row's candidates in rank order, at most n. */
export function topCandidates(row: BatchRow, n = 3): Candidate[] {
  return [...(row.candidates ?? [])]
    .sort((a, b) => a.rank - b.rank)
    .slice(0, n);
}

export type RowFilter = { status: RowStatus | ""; noCandidateReaches: boolean };

/** rowsQuery builds the ?status=&no_candidate_reaches= query string. */
export function rowsQuery(f: RowFilter): string {
  const qs = new URLSearchParams();
  if (f.status) qs.set("status", f.status);
  if (f.noCandidateReaches) qs.set("no_candidate_reaches", "true");
  const s = qs.toString();
  return s ? `?${s}` : "";
}

/** approveClearMessage summarises a bulk approve-clear for a toast. */
export function approveClearMessage(r: ApproveClearResult): string {
  const dup = r.duplicates?.length ?? 0;
  const skipped = r.skipped?.length ?? 0;
  const parts = [
    `Approved ${r.approved_count} of ${r.qualifying} clear ${
      r.qualifying === 1 ? "row" : "rows"
    }`,
  ];
  if (dup > 0) parts.push(`${dup} marked duplicate`);
  if (skipped > 0) parts.push(`${skipped} skipped`);
  return parts.join("; ");
}

/** parseApplicability reads the sheet's applicability (stored as JSON
 *  text) for the scope picker. Unparseable text yields undefined, which
 *  the picker shows as Everything; the server still validates. */
export function parseApplicability(raw: string | undefined): unknown {
  if (!raw || raw.trim() === "") return undefined;
  try {
    return JSON.parse(raw);
  } catch {
    return undefined;
  }
}

export function batchPath(id: string): string {
  return `/api/controls/imports/${encodeURIComponent(id)}`;
}

export function rowPath(batchID: string, rowID: string, verb: string): string {
  return `${batchPath(batchID)}/rows/${encodeURIComponent(rowID)}/${verb}`;
}

/** postJSON posts to a control-import BFF route and returns the parsed
 *  body, or throws an APIError carrying the platform's status and error
 *  message (409 "already running", 400 "reason is required", ...). */
export async function postJSON<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method: "POST",
    headers:
      body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const parsed = (await res.json().catch(() => ({}))) as { error?: string };
  if (!res.ok) {
    throw new APIError(
      res.status,
      parsed.error ?? `request failed (${res.status})`,
    );
  }
  return parsed as T;
}
