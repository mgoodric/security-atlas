// Slice 125 — BFF for the slice-124 unified audit-log aggregation endpoint.
//
// AC-6. This route forwards the browser's `GET /api/audit-log/unified?...`
// query string verbatim to the platform's
// `GET /v1/admin/audit-log/unified?...` endpoint and pipes the response
// back. The platform handler (internal/api/adminauditlog/unified.go) is
// responsible for ALL validation (90-day window guard, kind whitelist,
// cursor parsing) and ALL authorization (admin OR auditor OR grc_engineer);
// this BFF is a pure passthrough with only the bearer attached.
//
// Slice 110 P0-A2 narrow-scope rule: the `atlas_session` cookie is forwarded
// ONLY on the /api/me/sessions* routes — broadening that surface here would
// leak the OIDC session id into a request path that does not need it. The
// unified audit-log endpoint authenticates on the bearer alone. We
// intentionally do NOT import `buildSessionsForwardHeaders`.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(request: Request): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  // Forward the query string verbatim. The platform handler owns parsing.
  const url = new URL(request.url);
  const upstreamURL = `${apiBaseURL()}/v1/admin/audit-log/unified${url.search}`;

  const upstream = await fetch(upstreamURL, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });

  const text = await upstream.text();
  try {
    return NextResponse.json(text ? JSON.parse(text) : null, {
      status: upstream.status,
    });
  } catch {
    // Non-JSON upstream body (rare) — pass through as-is so the user can
    // see what the platform returned rather than swallowing it.
    return new NextResponse(text, { status: upstream.status });
  }
}
