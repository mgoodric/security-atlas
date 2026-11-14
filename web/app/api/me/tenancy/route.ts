// #1764 — BFF proxy for GET /v1/me/tenancy (#1763).
//
// Forwards the atlas_jwt cookie to the platform, which answers with the
// install's tenant count. The web app reads it (`lib/tenancy.ts`) to
// decide whether to show tenant chrome. It names no tenant, only how
// many exist.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/app/oauth/callback/route";

export async function GET(): Promise<Response> {
  const jar = await cookies();
  const jwt = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!jwt) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(`${apiBaseURL()}/v1/me/tenancy`, {
    headers: { Authorization: `Bearer ${jwt}` },
    cache: "no-store",
  });
  const text = await upstream.text();
  try {
    return NextResponse.json(text ? JSON.parse(text) : null, {
      status: upstream.status,
    });
  } catch {
    return new NextResponse(text, {
      status: upstream.status,
      headers: { "Content-Type": "text/plain" },
    });
  }
}
