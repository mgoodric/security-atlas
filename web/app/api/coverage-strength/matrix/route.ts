import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { getCoverageStrengthMatrix } from "@/lib/api/coverage-strength-matrix";

// OE-473 / slice 402a — BFF pass-through for the backend matrix read
// model. RLS and aggregation live upstream; this route forwards only the
// httpOnly bearer and optional paging/filter query string.

export async function GET(req: NextRequest) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  try {
    const matrix = await getCoverageStrengthMatrix(
      bearer,
      req.nextUrl.searchParams.toString(),
    );
    return NextResponse.json(matrix);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
