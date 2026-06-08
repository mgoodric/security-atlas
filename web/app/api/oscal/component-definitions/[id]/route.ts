import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { getComponentDefinition } from "@/lib/api/oscal-components";

// Server-side proxy: one imported component-definition + its vendor claims.
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
    const body = await getComponentDefinition(bearer, id);
    return NextResponse.json(body);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
