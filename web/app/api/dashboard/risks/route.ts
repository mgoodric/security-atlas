import { getMitigateRisks } from "@/lib/api/dashboard";
import { dashboardProxy } from "../proxy";

// Slice 157 — server-side proxy for GET /v1/risks?treatment=mitigate
// &sort=residual,age (slice 066 AC-3 — ListRisks gained the
// residual,age sort capability).
//
// Slice 040 wired this BFF to the unsorted `?treatment=mitigate` list
// and surfaced the ranking gap as a labelled `top-risks-sort-gap`
// footer because the residual,age sort had not yet shipped. Slice 066
// shipped the server-side sort; this slice (the spillover from slice
// 147) re-points the dashboard onto it.
//
// `getMitigateRisks` in lib/api.ts now appends `&sort=residual,age` to
// the upstream URL; this route stays a thin proxy that forwards the
// bearer and packs the result back into the legacy
// `{risks, count}` envelope the dashboard panel expects.

export function GET() {
  return dashboardProxy(async (bearer) => {
    const risks = await getMitigateRisks(bearer);
    return { risks, count: risks.length };
  });
}
