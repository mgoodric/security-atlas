// Slice 042 — shared BFF forwarding helper for the audit workspace.
//
// Every audit BFF route does the same thing: read the bearer cookie,
// forward to the platform with an Authorization header, pass the
// upstream status + JSON body through verbatim. This helper centralizes
// that so each route file stays a thin declaration.
//
// Tenant derivation: the platform derives the tenant from the calling
// credential — these routes never pass a tenant_id (slice 051 D1). The
// credential is also what scopes the auditor to their assigned period
// (P0-1) and filters note visibility server-side (P0-2). The BFF adds no
// filtering of its own.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

type ForwardInit = {
  method?: string;
  // JSON body to forward. Omit for GET. For multipart use forwardMultipart.
  jsonBody?: unknown;
};

// forwardJSON proxies a JSON request to the platform. Returns a
// NextResponse carrying the upstream status + body verbatim.
export async function forwardJSON(
  upstreamPath: string,
  init?: ForwardInit,
): Promise<NextResponse> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const headers: Record<string, string> = {
    Authorization: `Bearer ${bearer}`,
  };
  let body: string | undefined;
  if (init?.jsonBody !== undefined) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(init.jsonBody);
  }
  const upstream = await fetch(`${apiBaseURL()}${upstreamPath}`, {
    method: init?.method ?? "GET",
    headers,
    body,
    cache: "no-store",
  });
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}

// forwardMultipart proxies a multipart/form-data request (walkthrough
// attachment upload) to the platform, streaming the FormData through.
export async function forwardMultipart(
  upstreamPath: string,
  form: FormData,
): Promise<NextResponse> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(`${apiBaseURL()}${upstreamPath}`, {
    method: "POST",
    headers: { Authorization: `Bearer ${bearer}` },
    body: form,
    cache: "no-store",
  });
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
