// Slice 108 — BFF proxy for /v1/me/sessions (GET list + DELETE all-others).
// Slice 110 — additionally forwards the slice-034 `atlas_session` cookie
// so the platform handler can flag the current session row (`is_current`).
// See `_headers.ts` for the narrow-scope rationale.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { OIDC_SESSION_COOKIE, SESSION_COOKIE } from "@/lib/auth";
import { buildSessionsForwardHeaders } from "./_headers";

export async function GET(): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const oidc = jar.get(OIDC_SESSION_COOKIE)?.value;
  const upstream = await fetch(`${apiBaseURL()}/v1/me/sessions`, {
    headers: buildSessionsForwardHeaders(bearer, oidc),
    cache: "no-store",
  });
  return passthrough(upstream);
}

export async function DELETE(): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const oidc = jar.get(OIDC_SESSION_COOKIE)?.value;
  const upstream = await fetch(`${apiBaseURL()}/v1/me/sessions`, {
    method: "DELETE",
    headers: buildSessionsForwardHeaders(bearer, oidc),
  });
  return passthrough(upstream);
}

async function passthrough(upstream: Response): Promise<Response> {
  const text = await upstream.text();
  if (upstream.status === 204) {
    return new NextResponse(null, { status: 204 });
  }
  try {
    return NextResponse.json(text ? JSON.parse(text) : null, {
      status: upstream.status,
    });
  } catch {
    return new NextResponse(text, { status: upstream.status });
  }
}
