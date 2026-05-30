// Slice 177 — BFF proxy for `/exceptions` list view.
//
// Reads the bearer cookie server-side and calls `GET /v1/exceptions[?status=&control_id=]`
// upstream. The bearer never reaches the browser. Mirrors the slice 099
// evidence + slice 101 policies BFF pattern (cookie -> bearer -> upstream
// call). The row source is `internal/api/exceptions/handlers.go`
// (`exceptionWire`) — the same wire shape the slice 138 export handler
// surfaces.
//
// Tenant isolation (Invariant 6): the platform derives the tenant from
// the bearer; this BFF never reads or forwards a tenant_id from the
// client. RLS denies cross-tenant reads at the DB layer. The BFF also
// explicitly whitelists the query params it forwards — arbitrary
// caller-supplied keys (`tenant_id`, `debug`, etc.) are dropped.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

// FORWARD_PARAMS are the query keys the BFF is willing to forward to
// upstream. Anything else (`tenant_id`, `debug`, etc.) is dropped so a
// malicious or buggy caller cannot leak a privilege-escalation hint
// through this proxy. The whitelist mirrors the upstream
// `internal/api/exceptions/handlers.go` ListExceptions handler, which
// accepts `?status=` (and `?control_id=` via the ListFilter struct).
const FORWARD_PARAMS = ["status", "control_id"];

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
      ? `${apiBaseURL()}/v1/exceptions?${qs}`
      : `${apiBaseURL()}/v1/exceptions`,
    {
      headers: { Authorization: `Bearer ${bearer}` },
      cache: "no-store",
    },
  );
  const body = await upstream.text();
  return new NextResponse(body, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
