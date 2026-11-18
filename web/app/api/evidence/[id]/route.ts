// #2137 — BFF proxy for the `/evidence/[id]` detail view.
//
// Reads the bearer cookie server-side and calls `GET /v1/evidence/{id}`
// upstream; the bearer never reaches the browser. The platform enforces
// tenant isolation (RLS) and access, answering 404 for both an unknown id
// and a record the caller may not read. This route forwards no
// client-supplied query params and no tenant_id.
//
// The upstream body and status pass through unchanged: the payload is
// already redacted server-side.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { rejectNonUUID } from "@/lib/api/bff";

export async function GET(
  _req: Request,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(
    `${apiBaseURL()}/v1/evidence/${encodeURIComponent(id)}`,
    {
      headers: { Authorization: `Bearer ${bearer}` },
      cache: "no-store",
    },
  );
  const body = await upstream.text();
  return new NextResponse(body, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
