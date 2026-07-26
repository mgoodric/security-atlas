// Slice 230 — dashboard snapshot export BFF.
//
// Forwards `GET /api/dashboard/export?format=<json|csv|xlsx>` to the
// platform's `GET /v1/dashboard/export?...` endpoint and streams the response
// body back. The platform owns the format whitelist, role gate, RLS-scoped panel
// reads, and meta-audit row; this route only attaches the httpOnly bearer.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

const PASSTHROUGH_HEADERS = [
  "content-type",
  "content-disposition",
  "content-length",
  "x-content-type-options",
  "retry-after",
];

export async function GET(request: Request): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const url = new URL(request.url);
  const upstreamURL = `${apiBaseURL()}/v1/dashboard/export${url.search}`;

  const upstream = await fetch(upstreamURL, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });

  if (!upstream.ok) {
    const text = await upstream.text();
    const headers: Record<string, string> = {
      "Content-Type":
        upstream.headers.get("Content-Type") ?? "application/json",
    };
    const retryAfter = upstream.headers.get("Retry-After");
    if (retryAfter) headers["Retry-After"] = retryAfter;
    return new NextResponse(text, { status: upstream.status, headers });
  }

  const headers: Record<string, string> = {};
  for (const name of PASSTHROUGH_HEADERS) {
    const value = upstream.headers.get(name);
    if (value !== null) headers[name] = value;
  }

  return new NextResponse(upstream.body, {
    status: upstream.status,
    headers,
  });
}
