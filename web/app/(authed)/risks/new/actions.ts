// Slice 105 — client-side wrapper for the risk-create POST.
//
// Mirrors slice 024's `createVendorFromCookieSession`: the call goes
// through the Next.js BFF route at `/api/risks` (POST handler added in
// slice 105) so the bearer cookie stays httpOnly. The BFF forwards to
// the slice-019 `POST /v1/risks` backend write path unchanged.

import { APIError, Risk, RiskCreateInput } from "@/lib/api";

export async function createRiskFromCookieSession(
  body: RiskCreateInput,
): Promise<Risk> {
  const res = await fetch("/api/risks", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    // Bubble the upstream {error} string when present — the slice-019
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
  const decoded = (await res.json()) as { risk: Risk };
  return decoded.risk;
}
