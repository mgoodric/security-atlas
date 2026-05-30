import { NextRequest } from "next/server";

import { getDecisions } from "@/lib/api/risk-hierarchy";
import { hierarchyProxy } from "../proxy";

// Slice 056 — server-side proxy for GET /v1/decisions (slice 055
// Decision Log). Forwards the optional `?status=` filter; the
// constraints / decision_maker / date-range filters are applied
// client-side over the returned rows (the upstream handler exposes only
// `?status=` and `?revisit_due_within_days=`). Pure read-only.

export async function GET(req: NextRequest) {
  const status = req.nextUrl.searchParams.get("status") ?? undefined;
  return hierarchyProxy((bearer) =>
    getDecisions(bearer, status).then((decisions) => ({
      decisions,
      count: decisions.length,
    })),
  );
}
