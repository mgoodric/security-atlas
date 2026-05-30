import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { getControlEffectiveScope } from "@/lib/api/control-detail";

// Slice 041 — server-side proxy for
// GET /v1/controls/{id}/effective-scope?framework_version=<UUID>
// (slice 018 FrameworkScope intersection). Drives the effective-scope
// right-rail panel and the out-of-scope styling of coverage-table rows.
//
// The `framework_version` query parameter is required by upstream and
// must be a framework_version UUID (the value comes from a coverage
// requirement's `framework_version_id`). The BFF validates only that it
// is present; the platform is the authority on UUID shape.

export async function GET(
  req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await ctx.params;
  const frameworkVersion = req.nextUrl.searchParams.get("framework_version");
  if (!frameworkVersion) {
    return NextResponse.json(
      { error: "framework_version query parameter is required" },
      { status: 400 },
    );
  }
  try {
    const scope = await getControlEffectiveScope(bearer, id, frameworkVersion);
    return NextResponse.json(scope);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
