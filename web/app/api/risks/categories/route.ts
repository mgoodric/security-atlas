// #1817 — BFF proxy for `GET /v1/risks/categories`.
//
// The organisation's risk categories. The `/risks/new` picker reads the
// active ones; the register filter and the risk detail read them with
// `include_archived=true`, so an archived category still has its name and
// an "archived" mark. Only `include_archived` is forwarded; upstream
// status + body pass through verbatim, so a bad value is upstream's 400.
// The bearer never reaches the browser.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiBaseURL } from "@/lib/api/base";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export async function GET(req: Request): Promise<Response> {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const includeArchived = new URL(req.url).searchParams.get("include_archived");
  const qs =
    includeArchived === null
      ? ""
      : `?include_archived=${encodeURIComponent(includeArchived)}`;
  const upstream = await fetch(`${apiBaseURL()}/v1/risks/categories${qs}`, {
    headers: { Authorization: `Bearer ${bearer}` },
    cache: "no-store",
  });
  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
