// Slice 042 — audit workspace BFF: GET /v1/me/audit-period proxy.
//
// Forwards the bearer cookie as an Authorization header to the platform.
// Upstream 404 (caller has no auditor assignment) is passed through as
// 404 so the UI renders the "no period assigned" empty-state rather than
// erroring. The platform scopes the response to the caller's UserID, so
// this BFF never needs to pass a tenant or user id (P0-1: the auditor
// only ever sees their own assigned period).

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api";
import { SESSION_COOKIE } from "@/lib/auth";

export async function GET() {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(`${apiBaseURL()}/v1/me/audit-period`, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });
  const body = await upstream.text();
  return new NextResponse(body, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
