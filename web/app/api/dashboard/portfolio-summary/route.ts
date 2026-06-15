import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import {
  getPortfolioEvidenceSummary,
  type PortfolioFilter,
} from "@/lib/api/portfolio-summary";

// Slice 750 — BFF proxy for GET /v1/evidence-summary/portfolio (the portfolio
// / multi-control AI evidence-summary). It reads the httpOnly bearer cookie,
// forwards the OPTIONAL filter query params (family OR framework_version_id +
// framework label) to the typed client, and passes the result (or the upstream
// error status) back to the browser. The bearer never reaches the client.
//
// Unlike the shared dashboardProxy (which takes a bearer-only loader), this route
// must thread the request's filter params through, so it inlines the same
// read-cookie / call / forward shape.
export async function GET(req: NextRequest): Promise<NextResponse> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const sp = req.nextUrl.searchParams;
  const filter: PortfolioFilter = {};
  const fv = sp.get("framework_version_id");
  if (fv) {
    filter.frameworkVersionID = fv;
    const label = sp.get("framework");
    if (label) filter.frameworkLabel = label;
  } else {
    const family = sp.get("family");
    if (family) filter.family = family;
  }

  try {
    return NextResponse.json(await getPortfolioEvidenceSummary(bearer, filter));
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
