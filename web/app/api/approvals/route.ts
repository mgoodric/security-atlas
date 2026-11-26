// #2119 — BFF proxy for GET /v1/approvals, what is waiting for the
// caller's approval. The /approvals page and the sidebar badge read it.
//
// Forwards the bearer cookie; the bearer never reaches the browser. The
// platform derives the tenant and the caller from the bearer and lists
// only what the caller could approve.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { upstreamBody } from "@/lib/api/bff";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(`${apiBaseURL()}/v1/approvals`, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });
  const body = await upstream.text();
  return new NextResponse(upstreamBody(upstream.status, body), {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
