import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { getRequirementCoverage } from "@/lib/api/requirement-coverage";

// Slice 482 — server-side proxy for GET /v1/requirements/{id}/coverage
// (slice 008 forward traversal + slice 482 coverage-strength rollup).
// Reads the httpOnly bearer cookie and forwards it as Authorization on
// the upstream call so the token never reaches the browser. Pure
// read-only — no request body. The rollup (coverage_strength +
// confidence_band) is computed upstream under RLS; this proxy is a pure
// pass-through and adds NO client-supplied input to the score
// (P0-482-4).

export async function GET(
  _req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await ctx.params;
  try {
    const coverage = await getRequirementCoverage(bearer, id);
    return NextResponse.json(coverage);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
