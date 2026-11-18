import { getTopRisks } from "@/lib/api/dashboard";
import { dashboardProxy } from "../proxy";

// #2135 — server-side proxy for the dashboard top-risks card:
// GET /v1/risks?status=open&include_rollups=false&sort=-residual&limit=5
// (TOP_RISKS_QUERY). Slice 157 had pointed it at treatment=mitigate with
// the residual,age sort; the card now lists open risks in any treatment,
// ranked as the register ranks them.
//
// The route stays a thin proxy that forwards the bearer and packs the
// result back into the `{risks, count}` envelope the panel expects.

export function GET() {
  return dashboardProxy(async (bearer) => {
    const risks = await getTopRisks(bearer);
    return { risks, count: risks.length };
  });
}
