import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { getVendorSpendRollup } from "@/lib/api/vendors";

export async function GET() {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const rollup = await getVendorSpendRollup(bearer);
    return NextResponse.json({ rollup });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
