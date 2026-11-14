import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

// #1794 — POST /v1/framework-scopes/{id}/applicability/import. The body is
// the CSV file itself (not multipart), so it goes upstream as text/csv
// rather than through forwardJSON. The platform answers 200 (some rows
// applied) or 422 (none did), both with {applied, rejected[]}; status and
// body pass through verbatim. The 4 MiB cap is the platform's.

export async function POST(
  req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await ctx.params;
  const upstream = await fetch(
    `${apiBaseURL()}/v1/framework-scopes/${encodeURIComponent(
      id,
    )}/applicability/import`,
    {
      method: "POST",
      headers: {
        Authorization: `Bearer ${bearer}`,
        "Content-Type": "text/csv",
      },
      body: await req.text(),
      cache: "no-store",
    },
  );
  return new NextResponse(await upstream.text(), {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
