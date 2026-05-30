// Slice 192 — BFF proxy for GET /v1/me/tenants.
//
// Forwards the atlas_jwt cookie (slice 189 D1 — the OAuth-issued
// JWT) to the platform's `/v1/me/tenants` handler. The platform
// reads the verified claim and returns the caller's
// available_tenants[] enriched with name metadata (P0-192-2).
//
// CACHE: this route does NOT cache; the frontend component
// `<TenantSwitcher>` is responsible for re-fetching at the D1
// cadence (60s) so membership-removed UX (AC-13) can detect
// eventual-eviction state changes promptly.

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
  const upstream = await fetch(`${apiBaseURL()}/v1/me/tenants`, {
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
