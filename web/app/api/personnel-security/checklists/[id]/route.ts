// OE-664 — BFF proxy for one personnel-security checklist (GET detail,
// checklist + items in a single round-trip). Forwards the bearer cookie
// to upstream `/v1/personnel-security/checklists/{id}` (OE-663). RLS
// enforces tenant isolation (invariant 6); a cross-tenant id resolves
// to a clean upstream 404, indistinguishable from a missing row.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(
  _req: Request,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await params;
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(
    `${apiBaseURL()}/v1/personnel-security/checklists/${encodeURIComponent(
      id,
    )}`,
    { headers: { Authorization: `Bearer ${bearer}` }, cache: "no-store" },
  );
  const body = await upstream.text();
  return new NextResponse(body, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
