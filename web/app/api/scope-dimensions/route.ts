// #2103 — BFF proxy for the tenant's scope dimensions.
//
// The scope-dimension picker (components/scope/scope-picker.tsx) reads
// this to list each built-in dimension's values. The bearer cookie is read
// server-side and forwarded to `/v1/scopes/dimensions`; it never reaches
// the browser. Sibling of /api/scope-cells.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { listScopeDimensions } from "@/lib/api/scope-dimensions";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  try {
    const dimensions = await listScopeDimensions(bearer);
    return NextResponse.json({ dimensions });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
