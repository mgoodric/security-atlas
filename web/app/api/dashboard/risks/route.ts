import { getMitigateRisks } from "@/lib/api";
import { dashboardProxy } from "../proxy";

// Slice 040 — server-side proxy for GET /v1/risks?treatment=mitigate
// (slice 019 risk register).
//
// The dashboard mockup asks for `?treatment=mitigate&sort=residual,age`.
// The ListRisks handler supports only treatment/category/methodology
// filters — there is no server-side `sort` param, and `residual_score`
// is an opaque JSON blob. This route binds only the `treatment=mitigate`
// filter that actually exists; the panel renders the returned rows
// honestly and does not fabricate an ordering. The `sort=residual,age`
// server capability is a tracked follow-up gap (see the slice 040
// decisions log).

export function GET() {
  return dashboardProxy(async (bearer) => {
    const risks = await getMitigateRisks(bearer);
    return { risks, count: risks.length };
  });
}
