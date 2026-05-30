import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import {
  createVendor,
  listVendors,
  VendorListFilter,
  VendorWrite,
} from "@/lib/api/vendors";

// Server-side proxy. Bearer cookie -> upstream Authorization header. The
// list filter is forwarded; the create body is forwarded verbatim.

export async function GET(req: NextRequest) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const filter: VendorListFilter = {};
  const c = req.nextUrl.searchParams.get("criticality");
  if (c === "low" || c === "medium" || c === "high") {
    filter.criticality = c;
  }
  if (req.nextUrl.searchParams.get("overdue") === "true") {
    filter.overdue = true;
  }
  const asOf = req.nextUrl.searchParams.get("as_of");
  if (asOf) filter.as_of = asOf;
  try {
    const vendors = await listVendors(bearer, filter);
    return NextResponse.json({ vendors });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}

export async function POST(req: NextRequest) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  let body: VendorWrite;
  try {
    body = (await req.json()) as VendorWrite;
  } catch {
    return NextResponse.json({ error: "invalid JSON" }, { status: 400 });
  }
  try {
    const vendor = await createVendor(bearer, body);
    return NextResponse.json({ vendor }, { status: 201 });
  } catch (err) {
    const e = err as { status?: number; message?: string };
    return NextResponse.json(
      { error: e.message ?? "upstream error" },
      { status: e.status ?? 500 },
    );
  }
}
