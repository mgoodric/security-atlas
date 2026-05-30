// Slice 108 — BFF proxy for /v1/me (GET + PATCH).
//
// Forwards the ATLAS_JWT_COOKIE bearer to the upstream platform; the bearer never
// reaches the browser. PATCH passes the body through verbatim. Per slice 108
// P0-A3 the BFF does NOT roundtrip the IdP — the upstream's GET /v1/me is the
// single source of profile truth.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(`${apiBaseURL()}/v1/me`, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });
  return passthrough(upstream);
}

export async function PATCH(req: Request): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const body = await req.text();
  const upstream = await fetch(`${apiBaseURL()}/v1/me`, {
    method: "PATCH",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
    body,
  });
  return passthrough(upstream);
}

async function passthrough(upstream: Response): Promise<Response> {
  const text = await upstream.text();
  try {
    return NextResponse.json(text ? JSON.parse(text) : null, {
      status: upstream.status,
    });
  } catch {
    // Non-JSON upstream — pass through as text.
    return new NextResponse(text, { status: upstream.status });
  }
}
