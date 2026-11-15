// #1785 — BFF for GET /v1/controls/imports/{id}/export: the batch's
// approved controls as a gzip-tar of control.yaml bundles (#1784).
//
// Streams the body through with the platform's Content-Type and
// Content-Disposition (it names the file); an error passes through as
// JSON. Same posture as the controls export BFF (../../../export).

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

export async function GET(
  _req: Request,
  ctx: { params: Promise<{ id: string }> },
): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { id } = await ctx.params;
  const upstream = await fetch(
    `${apiBaseURL()}/v1/controls/imports/${encodeURIComponent(id)}/export`,
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
