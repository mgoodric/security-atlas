// Slice 370 — /audits list view (slice 102), extracted from the former
// `web/lib/api.ts` god-file. Named `audit-periods.ts` to avoid colliding
// with the pre-existing `lib/api/audit.ts` (audit workspace) and
// `lib/api/audit-server.ts` / `lib/api/audit-log.ts` siblings.

import { APIError } from "./base";
import { upstreamErrorMessage } from "./_shared";

// ----- Slice 102: /audits list view (browser-side BFF call) -----
//
// Row source: `periodWire` in `internal/api/auditperiods/handlers.go`
// (the canonical mapping per design doc §7). The page at
// `web/app/(authed)/audits/page.tsx` calls `fetchAuditPeriods` from the
// browser; the BFF at `web/app/api/audits/route.ts` is the server-side
// counterpart that injects the bearer cookie (slice 094 pattern).
//
// `frozen_at`, `frozen_hash`, `frozen_by` are present on the wire ONLY
// when the period is frozen (omitempty on the Go side). The TypeScript
// types reflect that with optional + nullable fields.
//
// `audit_periods.status` is constrained to `('open', 'frozen')` in v1
// per migration `20260511000020_audit_periods.sql`. The slice text
// mentions `planned/in-progress/frozen/closed` as forward-looking
// statuses; the page renders whatever status the backend returns and
// treats anything non-`frozen` as "live" for the in-progress amber-dot
// cue. This is forward-compatible: when the backend lifts the CHECK
// constraint to include more statuses, the renderer keeps working.

export type AuditPeriod = {
  id: string;
  name: string;
  framework_version_id: string;
  // Slice 680 / ATLAS-033: readable "<name> <version>" catalog label
  // (e.g. "SCF 2025.2"), present on the LIST path. Optional (omitempty
  // on the Go wire): absent when the framework version no longer
  // resolves. The /audits view renders this in place of a truncated
  // framework_version_id UUID, falling back to the UUID when absent.
  framework_label?: string;
  period_start: string;
  period_end: string;
  status: string;
  frozen_at?: string | null;
  frozen_hash?: string | null;
  frozen_by?: string | null;
  // #1742: a frozen period's latest freeze revision and its hash. 1 is
  // the original freeze; above 1, a historical import re-froze the period
  // with more evidence, and amended_by_import is true. frozen_hash stays
  // the original freeze's hash. Absent (omitempty) on an open period;
  // amended_by_import is always present.
  freeze_revision?: number;
  revision_hash?: string;
  amended_by_import?: boolean;
  // The admin's reason for the latest amendment (present when amended).
  amend_reason?: string;
  created_by: string;
  created_at: string;
  updated_at: string;
};

export type AuditPeriodsListResponse = {
  audit_periods: AuditPeriod[];
  count: number;
};

export async function fetchAuditPeriods(): Promise<AuditPeriodsListResponse> {
  const res = await fetch(`/api/audits`);
  if (!res.ok) {
    const msg = await upstreamErrorMessage(res);
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as AuditPeriodsListResponse;
}

// ----- #2117: /audits/[id] detail page (browser-side BFF calls) -----
//
// One period, its freeze, and its per-control horizon read. Each goes
// through a BFF under `web/app/api/audits/[id]/` that forwards the
// bearer cookie to `internal/api/auditperiods/handlers.go`.

export type AuditPeriodResponse = { audit_period: AuditPeriod };

// The platform's `controlStateObservationWire`: one evidence record
// inside the period's horizon (observed_at <= COALESCE(frozen_at, now)).
export type ControlStateObservation = {
  evidence_record_id: string;
  observed_at: string;
  result: string;
  hash: string;
};

export type ControlStateResponse = {
  audit_period_id: string;
  control_id: string;
  observations: ControlStateObservation[];
  count: number;
};

async function throwAPIError(res: Response): Promise<never> {
  const msg = await upstreamErrorMessage(res.clone());
  let code: string | undefined;
  try {
    code = ((await res.json()) as { code?: string }).code;
  } catch {
    /* body not JSON — no code */
  }
  throw new APIError(res.status, msg, code);
}

function periodPath(id: string): string {
  return `/api/audits/${encodeURIComponent(id)}`;
}

export async function fetchAuditPeriod(
  id: string,
): Promise<AuditPeriodResponse> {
  const res = await fetch(periodPath(id));
  if (!res.ok) return throwAPIError(res);
  return (await res.json()) as AuditPeriodResponse;
}

/** Freezes the period. Irreversible. The platform answers 409 when it
 *  is already frozen and 403 for a role it does not admit. */
export async function freezeAuditPeriod(
  id: string,
): Promise<AuditPeriodResponse> {
  const res = await fetch(`${periodPath(id)}/freeze`, { method: "POST" });
  if (!res.ok) return throwAPIError(res);
  return (await res.json()) as AuditPeriodResponse;
}

export async function fetchPeriodControlState(
  id: string,
  controlID: string,
): Promise<ControlStateResponse> {
  const qs = new URLSearchParams({ control: controlID });
  const res = await fetch(`${periodPath(id)}/control-state?${qs.toString()}`);
  if (!res.ok) return throwAPIError(res);
  return (await res.json()) as ControlStateResponse;
}

/** Fetches the signed OSCAL bundle through the download BFF. Throws
 *  APIError on a non-2xx so the caller can tell the bridge being absent
 *  (the platform route is not mounted) from a real refusal. */
export async function fetchOscalBundle(id: string): Promise<Blob> {
  const res = await fetch(`${periodPath(id)}/oscal-export`);
  if (!res.ok) return throwAPIError(res);
  return await res.blob();
}

// ----- #2118: the period's auditors (admin only) -----
//
// The platform lists the assigned auditors and the candidates: the
// tenant's active auditors not yet on the period. It refuses a non-admin
// with 403, which the page shows instead of the picker.

export type PeriodAuditor = {
  user_id: string;
  display_name: string;
  email: string;
  granted_at: string;
  granted_by: string;
};

export type AuditorCandidate = {
  user_id: string;
  display_name: string;
  email: string;
};

export type PeriodAuditorsResponse = {
  audit_period_id: string;
  auditors: PeriodAuditor[];
  count: number;
  candidates: AuditorCandidate[];
};

export async function fetchPeriodAuditors(
  id: string,
): Promise<PeriodAuditorsResponse> {
  const res = await fetch(`${periodPath(id)}/auditors`);
  if (!res.ok) return throwAPIError(res);
  return (await res.json()) as PeriodAuditorsResponse;
}

export async function assignPeriodAuditor(
  id: string,
  userID: string,
): Promise<void> {
  const res = await fetch(`${periodPath(id)}/auditors`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ user_id: userID }),
  });
  if (!res.ok) return throwAPIError(res);
}

export async function removePeriodAuditor(
  id: string,
  userID: string,
): Promise<void> {
  const res = await fetch(
    `${periodPath(id)}/auditors/${encodeURIComponent(userID)}`,
    { method: "DELETE" },
  );
  if (!res.ok) return throwAPIError(res);
}
