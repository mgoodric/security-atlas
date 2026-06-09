import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { getCsfGap } from "@/lib/api/csf";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

// Slice 515 — NIST CSF 2.0 Current-vs-Target gap view. Read-only BFF GET;
// same cookie-bearer pattern as /api/framework-scopes/route.ts.
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
    const gap = await getCsfGap(bearer, fv);
    return NextResponse.json(gap);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
