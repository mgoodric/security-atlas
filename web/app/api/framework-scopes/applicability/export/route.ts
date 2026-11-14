import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

// #1794 — BFF for the Statement of Applicability export (#1791).
//
// Forwards GET /api/framework-scopes/applicability/export?framework_version=
// …&format=csv|xlsx[&revision=…|&as_of=…] to the platform and streams the
// file back, in the same posture as the risk-register export BFF
// (/api/risks/export): bearer only, no buffering, and the backend authors
// the filename through Content-Disposition.

const PASSTHROUGH_HEADERS = [
  "content-type",
  "content-disposition",
  "content-length",
  "x-content-type-options",
];

export async function GET(request: Request): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const url = new URL(request.url);
  const upstream = await fetch(
    `${apiBaseURL()}/v1/framework-scopes/applicability/export${url.search}`,
    { headers: { Authorization: `Bearer ${bearer}` }, cache: "no-store" },
  );
  if (!upstream.ok) {
    return new NextResponse(await upstream.text(), {
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
    if (v !== null) headers[name] = v;
  }
  return new NextResponse(upstream.body, { status: upstream.status, headers });
}
