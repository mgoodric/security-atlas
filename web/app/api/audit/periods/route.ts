// Slice 042 — audit workspace BFF: GET /v1/me/audit-periods proxy.
//
// Returns the full list of the caller's auditor assignments (slice 025
// AC-6) so the workspace can offer historical-engagement switching. The
// platform scopes to caller.UserID — P0-1: cross-period data is never
// reachable, the list contains only the caller's own assignments.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET() {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(`${apiBaseURL()}/v1/me/audit-periods`, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });
  const body = await upstream.text();
  return new NextResponse(body, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
