// Slice 192 — BFF route for the tenant switch action.
//
// Receives a POST from the frontend `<TenantSwitcher>` component
// with `{ target_tenant_id }`. Reads the current atlas_jwt cookie,
// calls the platform's `/oauth/token` with the RFC 8693
// token-exchange grant (slice 188), and on success rewrites the
// atlas_jwt cookie with the freshly-minted JWT. Returns 200 + an
// empty JSON body to the caller; the caller is responsible for
// `router.refresh()` after a 200.
//
// CONSTITUTIONAL INVARIANTS HONORED:
//
//   - P0-192-3: the BFF route is a PURE proxy + cookie-swapper. It
//     does NOT mint, parse, or rewrite the JWT body — only the
//     atlas_jwt cookie value carrying the token is rotated. The
//     token-exchange contract is exclusively with the platform's
//     /oauth/token handler (slice 188).
//   - P0-192-6: this route does NOT modify slice 188's
//     /oauth/token handler — it is a pure consumer.
//   - P0-192-9: no per-tenant URL routing; the cookie carries the
//     tenant scope. Same path = re-rendered server components.
//
// SECURITY:
//   - HttpOnly cookie: the JWT never reaches the browser's JS
//     surface; switch-tenant.ts in the client only sees opaque
//     success/failure. Mirrors slice 189's HttpOnly discipline.
//   - SameSite=Lax + Secure-in-production per slice 189 P0-189-9.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import {
  ATLAS_JWT_COOKIE,
  ATLAS_JWT_COOKIE_LIFETIME_SECONDS,
} from "@/app/oauth/callback/route";

const TOKEN_EXCHANGE_GRANT_TYPE =
  "urn:ietf:params:oauth:grant-type:token-exchange";
const SUBJECT_TOKEN_TYPE_JWT = "urn:ietf:params:oauth:token-type:jwt";

interface SwitchRequest {
  target_tenant_id?: string;
}

export async function POST(req: Request): Promise<Response> {
  const jar = await cookies();
  const currentJwt = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!currentJwt) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  let body: SwitchRequest;
  try {
    body = (await req.json()) as SwitchRequest;
  } catch {
    return NextResponse.json({ error: "invalid_json" }, { status: 400 });
  }
  const targetTenantId = body.target_tenant_id?.trim();
  if (!targetTenantId) {
    return NextResponse.json(
      { error: "missing target_tenant_id" },
      { status: 400 },
    );
  }
  // Light-weight UUID shape check; the platform's /oauth/token
  // rejects malformed UUIDs with 400, but doing it here avoids one
  // extra round-trip on obviously-bad input.
  if (!isUuidShape(targetTenantId)) {
    return NextResponse.json(
      { error: "target_tenant_id is not a valid UUID" },
      { status: 400 },
    );
  }

  const form = new URLSearchParams();
  form.set("grant_type", TOKEN_EXCHANGE_GRANT_TYPE);
  form.set("subject_token", currentJwt);
  form.set("subject_token_type", SUBJECT_TOKEN_TYPE_JWT);
  form.set("atlas:target_tenant_id", targetTenantId);

  let upstream: Response;
  try {
    upstream = await fetch(`${apiBaseURL()}/oauth/token`, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: form.toString(),
      cache: "no-store",
    });
  } catch (e) {
    return NextResponse.json(
      {
        error: "token_exchange_network_error",
        detail: e instanceof Error ? e.message : "network error",
      },
      { status: 502 },
    );
  }

  if (!upstream.ok) {
    const text = await upstream.text();
    return new NextResponse(text || `${upstream.status}`, {
      status: upstream.status,
      headers: { "Content-Type": "application/json" },
    });
  }

  let parsed: { access_token?: string; expires_in?: number };
  try {
    parsed = (await upstream.json()) as {
      access_token?: string;
      expires_in?: number;
    };
  } catch {
    return NextResponse.json(
      { error: "upstream_invalid_json" },
      { status: 502 },
    );
  }
  if (!parsed.access_token || typeof parsed.access_token !== "string") {
    return NextResponse.json(
      { error: "upstream_missing_access_token" },
      { status: 502 },
    );
  }

  const maxAge =
    typeof parsed.expires_in === "number" && parsed.expires_in > 0
      ? parsed.expires_in
      : ATLAS_JWT_COOKIE_LIFETIME_SECONDS;

  const resp = NextResponse.json({ ok: true });
  resp.cookies.set({
    name: ATLAS_JWT_COOKIE,
    value: parsed.access_token,
    httpOnly: true,
    secure: process.env.NODE_ENV === "production",
    sameSite: "lax",
    path: "/",
    maxAge,
  });
  return resp;
}

// isUuidShape checks the RFC 4122 36-char hyphenated form. The
// platform does its own strict validation; this is a cheap filter.
function isUuidShape(s: string): boolean {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
    s,
  );
}
