// Slice 149 — client-side wrapper for the audit-period create POST.
//
// Mirrors slice 105's `createRiskFromCookieSession`: the call goes
// through the Next.js BFF route at `/api/audits` (POST handler added in
// slice 149) so the bearer cookie stays httpOnly. The BFF forwards to
// the slice-028 `POST /v1/audit-periods` backend write path unchanged.

import { APIError } from "@/lib/api/base";

// Wire shape mirrors `createReq` in
// `internal/api/auditperiods/handlers.go` exactly. All four fields are
// required; the backend rejects empty name, missing UUID, zero times,
// or period_start > period_end with a 400 (see handlers.go lines 104-115).
export type AuditPeriodCreateInput = {
  name: string;
  framework_version_id: string;
  period_start: string;
  period_end: string;
};

// Returned `periodWire` shape from the upstream Create handler — only
// the fields the create flow needs to confirm success. The full shape
// is in handlers.go `periodWire`.
export type AuditPeriodCreated = {
  id: string;
  name: string;
  status: string;
};

export async function createAuditPeriodFromCookieSession(
  body: AuditPeriodCreateInput,
): Promise<AuditPeriodCreated> {
  const res = await fetch("/api/audits", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    // Bubble the upstream {error} string when present — the slice-028
    // handler returns `{"error": "<msg>"}` on every 4xx. The form
    // surfaces this inline without losing the user's input.
    let msg = `${res.status} ${res.statusText}`;
    try {
      const j = (await res.json()) as { error?: string };
      if (j.error) msg = j.error;
    } catch {
      // body not JSON; keep the status line
    }
    throw new APIError(res.status, msg);
  }
  // The Create handler returns the periodWire object directly (not
  // wrapped in a `{period: ...}` envelope) — see handlers.go.
  return (await res.json()) as AuditPeriodCreated;
}
