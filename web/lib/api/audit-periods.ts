// Slice 370 — /audits list view (slice 102), extracted from the former
// `web/lib/api.ts` god-file. Named `audit-periods.ts` to avoid colliding
// with the pre-existing `lib/api/audit.ts` (audit workspace) and
// `lib/api/audit-server.ts` / `lib/api/audit-log.ts` siblings.

import { APIError } from "./base";

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
  period_start: string;
  period_end: string;
  status: string;
  frozen_at?: string | null;
  frozen_hash?: string | null;
  frozen_by?: string | null;
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
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      /* body not JSON — keep the status line */
    }
    throw new APIError(res.status, msg);
  }
  return (await res.json()) as AuditPeriodsListResponse;
}
