// Slice 144 — BFF proxy for PATCH /v1/tenants/{id}.
//
// Forwards the atlas_jwt cookie (slice 189 D1 — the OAuth-issued
// JWT) to the platform's `/v1/tenants/{id}` handler. The platform
// reads the verified claim, validates the caller's authority
// (per-tenant admin OR super_admin), and on success returns the
// updated tenant row.
//
// CONSTITUTIONAL INVARIANTS HONORED:
//
//   - P0-RT-3: this BFF route is a PURE proxy. It does NOT mint,
//     parse, or rewrite the JWT body — only forwards. Authority
//     gating happens on the platform side.
//   - P0-RT-4: only `name` is forwarded in the body; the platform
//     ignores any other top-level keys per the slice-144 handler's
//     `patchTenantRequest` shape (only `Name` is decoded).
//
// SECURITY:
//   - HttpOnly cookie: the JWT never reaches the browser's JS
//     surface; the client only sees opaque success/failure JSON.

import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/app/oauth/callback/route";

export async function PATCH(
  req: NextRequest,
  { params }: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await params;
  const jar = await cookies();
  const jwt = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!jwt) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  // Pass through the raw body. The platform handler is the canonical
  // validator of shape + UTF-8 + length cap; preserving the original
  // bytes lets server-side error messages line up with what the user
  // typed.
  const body = await req.text();

  const upstream = await fetch(
    `${apiBaseURL()}/v1/tenants/${encodeURIComponent(id)}`,
    {
      method: "PATCH",
      headers: {
        Authorization: `Bearer ${jwt}`,
        "Content-Type": "application/json",
      },
      body,
      cache: "no-store",
    },
  );
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
