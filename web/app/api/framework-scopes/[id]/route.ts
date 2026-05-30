import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { patchFrameworkScopePredicate } from "@/lib/api/framework-scopes";

// Slice 018 — PATCH /v1/framework-scopes/{id} (edit predicate). The
// upstream trigger may bounce state back to draft; we forward
// `approval_invalidated` verbatim so the page can render the banner.

export async function PATCH(
  req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await ctx.params;
  let body: { predicate?: unknown };
  try {
    body = (await req.json()) as { predicate?: unknown };
  } catch {
    return NextResponse.json({ error: "invalid JSON" }, { status: 400 });
  }
  if (body.predicate === undefined) {
    return NextResponse.json(
      { error: "predicate is required" },
      { status: 400 },
    );
  }
  try {
    const out = await patchFrameworkScopePredicate(bearer, id, body.predicate);
    return NextResponse.json(out);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
