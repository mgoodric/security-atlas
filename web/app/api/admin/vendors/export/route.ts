// Slice 139 — BFF for the vendor data-export endpoint.
//
// Forwards `GET /api/admin/vendors/export?format=<csv|json|xlsx>` to
// the platform `GET /v1/admin/vendors/export?...` and STREAMS the
// response back. Sibling of `web/app/api/admin/audit-periods/export/`
// — identical posture, different upstream path.
//
// See the audit-periods BFF for the full posture commentary; the only
// per-entity nuance the vendor export adds is the `owner_user_masked`
// column rendering. That happens entirely server-side in the platform
// handler — the BFF sees only the encoded bytes.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { SESSION_COOKIE } from "@/lib/auth";

const PASSTHROUGH_HEADERS = [
  "content-type",
  "content-disposition",
  "content-length",
  "x-content-type-options",
];

export async function GET(request: Request): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const url = new URL(request.url);
  const upstreamURL = `${apiBaseURL()}/v1/admin/vendors/export${url.search}`;

  const upstream = await fetch(upstreamURL, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });

  if (!upstream.ok) {
    const text = await upstream.text();
    return new NextResponse(text, {
      status: upstream.status,
      headers: {
        "Content-Type":
          upstream.headers.get("Content-Type") ?? "application/json",
      },
    });
  }

  const headers: Record<string, string> = {};
  for (const name of PASSTHROUGH_HEADERS) {
    const v = upstream.headers.get(name);
    if (v !== null) {
      headers[name] = v;
    }
  }
  return new NextResponse(upstream.body, {
    status: upstream.status,
    headers,
  });
}
