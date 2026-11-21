// #2110 — BFF for GET /v1/controls/{id}/bundle: Download bundle.
//
// Streams the control's gzip-tar bundle (control.yaml at the root, the
// shape the bundle upload reads back) through with the platform's
// Content-Type and Content-Disposition, which names the file; an error
// passes through as JSON. Same posture as the import export BFF
// (../../imports/[id]/export).

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { rejectNonUUID, upstreamBody } from "@/lib/api/bff";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

const PASSTHROUGH_HEADERS = [
  "content-type",
  "content-disposition",
  "content-length",
  "x-content-type-options",
];

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const upstream = await fetch(`${apiBaseURL()}/v1/controls/${id}/bundle`, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });
  if (!upstream.ok) {
    return new NextResponse(
      upstreamBody(upstream.status, await upstream.text()),
      {
        status: upstream.status,
        headers: {
          "Content-Type":
            upstream.headers.get("Content-Type") ?? "application/json",
        },
      },
    );
  }
  const headers: Record<string, string> = {};
  for (const name of PASSTHROUGH_HEADERS) {
    const v = upstream.headers.get(name);
    if (v !== null) headers[name] = v;
  }
  return new NextResponse(upstream.body, { status: upstream.status, headers });
}
