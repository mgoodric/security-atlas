import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { getCsfTier } from "@/lib/api/csf";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

// Slice 515 — read the tenant's CSF 2.0 Tier rating. Read-only BFF GET.
export async function GET(req: Request) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const fv = new URL(req.url).searchParams.get("framework_version");
  if (!fv) {
    return NextResponse.json(
      { error: "framework_version is required" },
      { status: 400 },
    );
  }
  try {
    const tier_rating = await getCsfTier(bearer, fv);
    return NextResponse.json({ tier_rating });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
