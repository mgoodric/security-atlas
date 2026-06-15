// Slice 384 — BFF proxy for the read-only "Linked Action Plans" section on
// `/controls/[id]` (AC-26). Forwards to upstream
// `/v1/controls/{id}/action-plans`. Read-only; only GET is exposed. The
// bearer never reaches the browser; RLS enforces tenant isolation
// (invariant 6).

import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(
  _req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await params;
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(
    `${apiBaseURL()}/v1/controls/${encodeURIComponent(id)}/action-plans`,
    { headers: { Authorization: `Bearer ${bearer}` }, cache: "no-store" },
  );
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
