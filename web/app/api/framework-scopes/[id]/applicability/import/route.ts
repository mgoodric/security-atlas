import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

// #1794 — POST /v1/framework-scopes/{id}/applicability/import. The body is
// the CSV file itself (not multipart), so it goes upstream as text/csv
// rather than through forwardJSON. The platform answers 200 (some rows
// applied) or 422 (none did), both with {applied, rejected[]}; status and
// body pass through verbatim. The 4 MiB cap is the platform's; a request
// that declares a larger body is refused here before it is buffered.

// MAX_IMPORT_BYTES mirrors maxImportBytes in
// internal/api/frameworkscopes/applicability_bulk.go.
const MAX_IMPORT_BYTES = 4 << 20;

export async function POST(
  req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const declared = Number(req.headers.get("content-length") ?? "0");
  if (declared > MAX_IMPORT_BYTES) {
    return NextResponse.json(
      { error: "import body exceeds 4 MiB" },
      { status: 413 },
    );
  }
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
