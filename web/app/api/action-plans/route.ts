// Slice 384 — BFF proxy for the `/action-plans` list + create.
//
// Reads the bearer cookie server-side and forwards to upstream
// `/v1/action-plans`. The bearer never reaches the browser. Mirrors the
// slice-177 `/api/exceptions` list BFF + the slice-100 `/api/risks` shape.
//
// Tenant isolation (invariant 6): the platform derives the tenant from the
// bearer; this BFF never reads or forwards a client-supplied tenant_id. The
// query whitelist drops arbitrary caller-supplied keys.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

// Query keys the BFF will forward to upstream GET /v1/action-plans.
const FORWARD_PARAMS = ["status", "limit", "cursor"];

export async function GET(req: Request): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const url = new URL(req.url);
  const out = new URLSearchParams();
  for (const key of FORWARD_PARAMS) {
    const v = url.searchParams.get(key);
    if (v) out.set(key, v);
  }
  const qs = out.toString();
  const upstream = await fetch(
    qs
      ? `${apiBaseURL()}/v1/action-plans?${qs}`
      : `${apiBaseURL()}/v1/action-plans`,
    { headers: { Authorization: `Bearer ${bearer}` }, cache: "no-store" },
  );
  const body = await upstream.text();
  return new NextResponse(body, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}

export async function POST(req: Request): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const payload = await req.text();
  const upstream = await fetch(`${apiBaseURL()}/v1/action-plans`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
    body: payload,
    cache: "no-store",
  });
  const body = await upstream.text();
  return new NextResponse(body, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
