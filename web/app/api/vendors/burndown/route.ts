import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { SESSION_COOKIE } from "@/lib/auth";
import { getVendorBurndown, VendorListFilter } from "@/lib/api";

export async function GET(req: NextRequest) {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const filter: VendorListFilter = {};
  const c = req.nextUrl.searchParams.get("criticality");
  if (c === "low" || c === "medium" || c === "high") {
    filter.criticality = c;
  }
  const asOf = req.nextUrl.searchParams.get("as_of");
  if (asOf) filter.as_of = asOf;
  try {
    const burndown = await getVendorBurndown(bearer, filter);
    return NextResponse.json(burndown);
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
