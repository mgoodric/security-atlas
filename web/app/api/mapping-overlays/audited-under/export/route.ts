// #1802 — BFF for GET /v1/mapping-overlays/audited-under/export: the
// report as CSV. Streams the body with the platform's Content-Type and
// Content-Disposition (it names the file); an error passes through as
// JSON. Only the `framework` filter is forwarded. Same posture as the
// anchors export BFF (../../../anchors/export).

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

const PASSTHROUGH_HEADERS = [
  "content-type",
  "content-disposition",
  "content-length",
  "x-content-type-options",
];

export async function GET(req: Request): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const framework = new URL(req.url).searchParams.get("framework");
  const qs = framework ? `?framework=${encodeURIComponent(framework)}` : "";
  const upstream = await fetch(
    `${apiBaseURL()}/v1/mapping-overlays/audited-under/export${qs}`,
    { headers: { Authorization: `Bearer ${bearer}` }, cache: "no-store" },
  );
  if (!upstream.ok) {
    return new NextResponse(await upstream.text(), {
      status: upstream.status,
      headers: {
        "Content-Type":
          upstream.headers.get("Content-Type") ?? "application/json",
        "Cache-Control": "no-store",
      },
    });
  }
  const headers: Record<string, string> = { "Cache-Control": "no-store" };
  for (const name of PASSTHROUGH_HEADERS) {
    const v = upstream.headers.get(name);
    if (v !== null) headers[name] = v;
  }
  return new NextResponse(upstream.body, { status: upstream.status, headers });
}
