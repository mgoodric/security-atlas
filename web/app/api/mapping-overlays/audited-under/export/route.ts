// #1802 — BFF for GET /v1/mapping-overlays/audited-under/export: the
// report as CSV. Streams the body with the platform's Content-Type and
// Content-Disposition (it names the file); an error passes through as
// JSON. Only the `framework` filter is forwarded, and only when it is
// shaped like a slug. Same posture as the
// anchors export BFF (../../../anchors/export).

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { isFrameworkSlug } from "@/lib/api/audited-under";
import { apiBaseURL } from "@/lib/api/base";
import { upstreamBody } from "@/lib/api/bff";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

const PASSTHROUGH_HEADERS = [
  "content-type",
  "content-disposition",
  "content-length",
  "x-content-type-options",
  // #2324: whether the rows were cut at the cap, and how many tested-under
  // references the report could not resolve.
  "x-atlas-truncated",
  "x-atlas-unresolved-references",
];

export async function GET(req: Request): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const framework = new URL(req.url).searchParams.get("framework");
  if (framework && !isFrameworkSlug(framework)) {
    return NextResponse.json(
      { error: "framework must be a framework slug" },
      { status: 400 },
    );
  }
  const qs = framework ? `?framework=${encodeURIComponent(framework)}` : "";
  const upstream = await fetch(
    `${apiBaseURL()}/v1/mapping-overlays/audited-under/export${qs}`,
    { headers: { Authorization: `Bearer ${bearer}` }, cache: "no-store" },
  );
  if (!upstream.ok) {
    return new NextResponse(
      upstreamBody(upstream.status, await upstream.text()),
      {
        status: upstream.status,
        headers: {
          "Content-Type":
            upstream.headers.get("Content-Type") ?? "application/json",
          "Cache-Control": "no-store",
        },
      },
    );
  }
  const headers: Record<string, string> = { "Cache-Control": "no-store" };
  for (const name of PASSTHROUGH_HEADERS) {
    const v = upstream.headers.get(name);
    if (v !== null) headers[name] = v;
  }
  return new NextResponse(upstream.body, { status: upstream.status, headers });
}
